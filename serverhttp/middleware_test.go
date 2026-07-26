package serverhttp_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func TestMiddlewareOrderRequestIDsAndBodyLimits(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

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
		requestID, ok := serverhttp.RequestID(request.Context())
		if !ok || requestID != "generated-id" {
			t.Fatalf("RequestID() = %q, %v", requestID, ok)
		}
		writer.WriteHeader(http.StatusNoContent)
	})
	runtime, err := serverhttp.New(
		listener,
		handler,
		serverhttp.WithBodyLimit(4),
		serverhttp.WithRequestIDs(serverhttp.RequestIDConfig{
			Generator: func() (string, error) { return "generated-id", nil },
		}),
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
	if got := recorder.Header().Get("X-Request-ID"); got != "generated-id" {
		t.Fatalf("response request ID = %q", got)
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

func TestRequestIDTrustRejectsHeaderInjection(t *testing.T) {
	t.Parallel()

	middleware, err := serverhttp.RequestIDs(serverhttp.RequestIDConfig{
		TrustInbound: true,
		Generator:    func() (string, error) { return "safe-id", nil },
	})
	if err != nil {
		t.Fatalf("RequestIDs() error = %v", err)
	}
	handler := middleware(http.HandlerFunc(func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		requestID, _ := serverhttp.RequestID(request.Context())
		_, _ = io.WriteString(writer, requestID)
	}))

	for name, inbound := range map[string]string{
		"trusted":   "trusted-id",
		"injection": "bad\r\nid",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("X-Request-ID", inbound)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, request)
			want := inbound
			if name == "injection" {
				want = "safe-id"
			}
			if body := recorder.Body.String(); body != want {
				t.Fatalf("body = %q, want %q", body, want)
			}
		})
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

func TestRequestIDAbsentOutsideMiddleware(t *testing.T) {
	t.Parallel()

	if requestID, ok := serverhttp.RequestID(context.Background()); ok || requestID != "" {
		t.Fatalf("RequestID() = %q, %v", requestID, ok)
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
	for name, config := range map[string]serverhttp.RequestIDConfig{
		"invalid header": {Header: "bad header"},
		"negative max":   {MaxLength: -1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := serverhttp.RequestIDs(config); !errors.Is(err, serverhttp.ErrInvalidConfig) {
				t.Fatalf("RequestIDs() error = %v", err)
			}
		})
	}

	for name, generator := range map[string]serverhttp.RequestIDGenerator{
		"failure": func() (string, error) { return "", errors.New("failed") },
		"invalid": func() (string, error) { return "bad id", nil },
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			middleware, err := serverhttp.RequestIDs(serverhttp.RequestIDConfig{
				Generator: generator,
			})
			if err != nil {
				t.Fatalf("RequestIDs() error = %v", err)
			}
			recorder := httptest.NewRecorder()
			middleware(nil).ServeHTTP(
				recorder,
				httptest.NewRequest(http.MethodGet, "/", nil),
			)
			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", recorder.Code)
			}
		})
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

func TestDefaultRequestIDIsValid(t *testing.T) {
	t.Parallel()

	middleware, err := serverhttp.RequestIDs(serverhttp.RequestIDConfig{})
	if err != nil {
		t.Fatalf("RequestIDs() error = %v", err)
	}
	recorder := httptest.NewRecorder()
	middleware(nil).ServeHTTP(
		recorder,
		httptest.NewRequest(http.MethodGet, "/", nil),
	)
	if requestID := recorder.Header().Get("X-Request-ID"); requestID == "" {
		t.Fatal("generated request ID is blank")
	}
}
