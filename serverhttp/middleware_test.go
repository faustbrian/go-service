package serverhttp_test

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/faustbrian/golib/pkg/correlation"
	httpcorrelation "github.com/faustbrian/golib/pkg/correlation/http"
	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func TestCorrelationAndIngressRunBeforeBodyRejection(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}

	ingressCalled := false
	handlerCalled := false
	ingress := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			values, ok := correlation.FromContext(request.Context())
			if !ok || values.CorrelationID == "" || values.RequestID == "" {
				t.Fatalf("correlation context = %#v, %v", values, ok)
			}
			ingressCalled = true
			next.ServeHTTP(writer, request)
		})
	}
	server, err := serverhttp.New(
		listener,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			handlerCalled = true
		}),
		serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
		serverhttp.WithIngressMiddleware(ingress),
		serverhttp.WithBodyLimit(1),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("too large"))
	server.HTTPServer().Handler.ServeHTTP(response, request)

	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", response.Code)
	}
	if !ingressCalled {
		t.Fatal("ingress middleware was skipped")
	}
	if handlerCalled {
		t.Fatal("handler ran after body rejection")
	}
	correlationID := response.Header().Get(httpcorrelation.CorrelationHeader)
	requestID := response.Header().Get(httpcorrelation.RequestHeader)
	if correlationID == "" || requestID == "" || correlationID == requestID {
		t.Fatalf("correlation = %q, request = %q", correlationID, requestID)
	}
}

func TestRecoveryPreservesSafeIdentityAndSecurityHeaders(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}
	security := func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("X-Content-Type-Options", "nosniff")
			next.ServeHTTP(writer, request)
		})
	}
	server, err := serverhttp.New(
		listener,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("X-Internal-Detail", "secret")
			panic("secret panic")
		}),
		serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
		serverhttp.WithMiddleware(security),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	response := httptest.NewRecorder()
	server.HTTPServer().Handler.ServeHTTP(
		response,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)

	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.Code)
	}
	if response.Header().Get("X-Internal-Detail") != "" {
		t.Fatalf("internal detail survived recovery: %q", response.Header())
	}
	if response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("nosniff = %q", response.Header().Get("X-Content-Type-Options"))
	}
	if response.Header().Get(httpcorrelation.CorrelationHeader) == "" ||
		response.Header().Get(httpcorrelation.RequestHeader) == "" {
		t.Fatalf("identity headers missing after panic: %q", response.Header())
	}
}

func TestMiddlewareOrderCorrelationAndBodyLimits(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}

	var events []string
	middleware := func(name string) serverhttp.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				events = append(events, "before "+name)
				next.ServeHTTP(writer, request)
				events = append(events, "after "+name)
			})
		}
	}
	handlerCalled := false
	handler := http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlerCalled = true
		values, ok := correlation.FromContext(request.Context())
		if !ok || values.CorrelationID == "" || values.RequestID == "" {
			t.Fatalf("correlation values = %#v, %v", values, ok)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	runtime, err := serverhttp.New(
		listener,
		handler,
		serverhttp.WithBodyLimit(4),
		serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
		serverhttp.WithMiddleware(middleware("first"), middleware("second")),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("four"))
	request.Header.Set("X-Request-ID", "untrusted-id")
	recorder := httptest.NewRecorder()
	runtime.HTTPServer().Handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
	if got := recorder.Header().Get(httpcorrelation.RequestHeader); got == "" {
		t.Fatal("response request ID is blank")
	}
	if !handlerCalled {
		t.Fatal("handler was not called")
	}
	wantEvents := []string{"before first", "before second", "after second", "after first"}
	if !reflect.DeepEqual(events, wantEvents) {
		t.Fatalf("events = %v, want %v", events, wantEvents)
	}

	tooLarge := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("large"))
	tooLargeRecorder := httptest.NewRecorder()
	runtime.HTTPServer().Handler.ServeHTTP(tooLargeRecorder, tooLarge)
	if tooLargeRecorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large status = %d, want %d",
			tooLargeRecorder.Code, http.StatusRequestEntityTooLarge)
	}
}

func TestDuplicateMiddlewareInstallationRemainsVisible(t *testing.T) {
	t.Parallel()

	var calls atomic.Int32
	middleware := serverhttp.Middleware(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			calls.Add(1)
			next.ServeHTTP(writer, request)
		})
	})
	handler, err := serverhttp.Chain(http.NotFoundHandler(), middleware, middleware)
	if err != nil {
		t.Fatalf("Chain() error = %v", err)
	}
	handler.ServeHTTP(
		httptest.NewRecorder(),
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if got := calls.Load(); got != 2 {
		t.Fatalf("middleware calls = %d, want 2", got)
	}
}

func TestRecoveryDoesNotLeakPanicOrPreparedHeaders(t *testing.T) {
	t.Parallel()

	handler := serverhttp.Recover()(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		writer.Header().Set("X-Secret", "secret")
		panic("secret panic")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusInternalServerError)
	}
	if recorder.Header().Get("X-Secret") != "" {
		t.Fatal("panic response retained a prepared secret header")
	}
	if strings.Contains(recorder.Body.String(), "secret") {
		t.Fatalf("panic response leaked value: %q", recorder.Body.String())
	}
}

func TestMiddlewareValidationAndFailurePaths(t *testing.T) {
	t.Parallel()

	if _, err := serverhttp.Chain(nil, nil); !errors.Is(err, serverhttp.ErrInvalidConfig) {
		t.Fatalf("Chain() nil middleware error = %v", err)
	}
	chained, err := serverhttp.Chain(nil)
	if err != nil {
		t.Fatalf("Chain(nil) error = %v", err)
	}
	chainRecorder := httptest.NewRecorder()
	chained.ServeHTTP(chainRecorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if chainRecorder.Code != http.StatusNotFound {
		t.Fatalf("Chain(nil) status = %d, want 404", chainRecorder.Code)
	}
	returnsNil := serverhttp.Middleware(func(http.Handler) http.Handler { return nil })
	if _, err := serverhttp.Chain(nil, returnsNil); !errors.Is(err, serverhttp.ErrInvalidConfig) {
		t.Fatalf("Chain() nil result error = %v", err)
	}
	if _, err := serverhttp.LimitBody(-1); !errors.Is(err, serverhttp.ErrInvalidConfig) {
		t.Fatalf("LimitBody() error = %v", err)
	}
}

func TestIdentityAndIngressOptionsRejectConflicts(t *testing.T) {
	t.Parallel()

	factory, err := correlation.NewFactory(correlation.FactoryOptions{})
	if err != nil {
		t.Fatalf("correlation.NewFactory() error = %v", err)
	}
	tests := map[string][]serverhttp.Option{
		"duplicate correlation": {
			serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
			serverhttp.WithCorrelation(factory, httpcorrelation.Options{}),
		},
		"nil correlation factory": {
			serverhttp.WithCorrelation(nil, httpcorrelation.Options{}),
		},
		"nil ingress middleware": {
			serverhttp.WithIngressMiddleware(nil),
		},
	}
	for name, options := range tests {
		listener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			t.Fatalf("%s Listen() error = %v", name, listenErr)
		}
		if _, newErr := serverhttp.New(
			listener,
			http.NotFoundHandler(),
			options...,
		); newErr == nil {
			_ = listener.Close()
			t.Fatalf("%s New() error = nil", name)
		}
		if closeErr := listener.Close(); closeErr != nil {
			t.Fatalf("%s listener ownership transferred: %v", name, closeErr)
		}
	}
}

func TestBodyLimitCoversStreamingAndDisabledBodies(t *testing.T) {
	t.Parallel()

	limited, err := serverhttp.LimitBody(4)
	if err != nil {
		t.Fatalf("LimitBody() error = %v", err)
	}
	var readError error
	handler := limited(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, readError = io.ReadAll(request.Body)
	}))
	request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("large"))
	request.ContentLength = -1
	handler.ServeHTTP(httptest.NewRecorder(), request)
	var maxBytesError *http.MaxBytesError
	if !errors.As(readError, &maxBytesError) {
		t.Fatalf("body read error = %v, want MaxBytesError", readError)
	}

	disabled, err := serverhttp.LimitBody(0)
	if err != nil {
		t.Fatalf("LimitBody(0) error = %v", err)
	}
	recorder := httptest.NewRecorder()
	disabled(nil).ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", nil))
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("disabled nil-handler status = %d, want 404", recorder.Code)
	}
}

func TestRecoveryPreservesCommittedResponseAndUnwrapsWriter(t *testing.T) {
	t.Parallel()

	flushed := false
	handler := serverhttp.Recover()(http.HandlerFunc(func(
		writer http.ResponseWriter,
		_ *http.Request,
	) {
		controller := http.NewResponseController(writer)
		if err := controller.Flush(); err != nil {
			t.Fatalf("Flush() error = %v", err)
		}
		flushed = true
		writer.WriteHeader(http.StatusAccepted)
		panic("hidden")
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if !flushed {
		t.Fatal("wrapped response was not flushed")
	}
	if recorder.Code != http.StatusOK {
		t.Fatalf("committed status = %d, want 200", recorder.Code)
	}

	nilRecorder := httptest.NewRecorder()
	serverhttp.Recover()(nil).ServeHTTP(
		nilRecorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if nilRecorder.Code != http.StatusNotFound {
		t.Fatalf("nil-handler status = %d, want 404", nilRecorder.Code)
	}
}
