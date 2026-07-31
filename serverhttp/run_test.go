package serverhttp_test

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func TestRunServesRealListenerAndDrainsActiveRequest(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	runtime, err := serverhttp.New(
		listener,
		http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			close(requestEntered)
			<-releaseRequest
			_, _ = io.WriteString(writer, "ok")
		}),
		serverhttp.WithShutdownTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(ctx) }()
	responseResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr != nil {
			responseResult <- requestErr

			return
		}
		defer func() { _ = response.Body.Close() }()
		body, readErr := io.ReadAll(response.Body)
		if readErr == nil && string(body) != "ok" {
			readErr = errors.New("unexpected response body")
		}
		responseResult <- readErr
	}()
	receiveTestValue(t, requestEntered)
	cancel()
	select {
	case err := <-runResult:
		t.Fatalf("Run() returned before active request drained: %v", err)
	default:
	}
	close(releaseRequest)
	if err := receiveTestValue(t, responseResult); err != nil {
		t.Fatalf("request error = %v", err)
	}
	if err := receiveTestValue(t, runResult); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunContextPropagatesToRequestHandlers(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	handlerEntered := make(chan struct{})
	handlerCause := make(chan error, 1)
	runtime, err := serverhttp.New(
		listener,
		http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			close(handlerEntered)
			<-request.Context().Done()
			handlerCause <- context.Cause(request.Context())
		}),
		serverhttp.WithShutdownTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runContext, cancelRun := context.WithCancelCause(context.Background())
	runCause := errors.New("service canceled")
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(runContext) }()
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		requestResult <- requestErr
	}()
	receiveTestValue(t, handlerEntered)
	cancelRun(runCause)
	if cause := receiveTestValue(t, handlerCause); !errors.Is(cause, runCause) {
		t.Fatalf("handler cause = %v, want run cause", cause)
	}
	if err := receiveTestValue(t, runResult); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	receiveTestValue(t, requestResult)
}

type contextMarker string

func TestRunAppliesExplicitBaseAndConnectionContexts(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	observed := make(chan [2]string, 1)
	runtime, err := serverhttp.New(
		listener,
		http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			observed <- [2]string{
				request.Context().Value(contextMarker("base")).(string),
				request.Context().Value(contextMarker("connection")).(string),
			}
			writer.WriteHeader(http.StatusNoContent)
		}),
		serverhttp.WithBaseContext(func(net.Listener) context.Context {
			return context.WithValue(
				context.Background(),
				contextMarker("base"),
				"base-value",
			)
		}),
		serverhttp.WithConnContext(func(ctx context.Context, _ net.Conn) context.Context {
			return context.WithValue(ctx, contextMarker("connection"), "connection-value")
		}),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runContext, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(runContext) }()
	response, err := http.Get("http://" + listener.Addr().String())
	if err != nil {
		t.Fatalf("http.Get() error = %v", err)
	}
	_ = response.Body.Close()
	if values := receiveTestValue(t, observed); values != [2]string{
		"base-value",
		"connection-value",
	} {
		t.Fatalf("context values = %v", values)
	}
	cancel()
	if err := receiveTestValue(t, runResult); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestRunReturnsTypedServeFailure(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	if err := listener.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	runtime, err := serverhttp.New(listener, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}

	err = runtime.Run(context.Background())
	var serveError *serverhttp.ServeError
	if !errors.As(err, &serveError) {
		t.Fatalf("Run() error = %v, want ServeError", err)
	}
	if err := runtime.Run(context.Background()); !errors.Is(err, serverhttp.ErrInvalidState) {
		t.Fatalf("second Run() error = %v, want ErrInvalidState", err)
	}
}

func TestRunRejectsNilContext(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	runtime, err := serverhttp.New(listener, nil)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	//lint:ignore SA1012 Public boundary must reject nil context safely.
	//nolint:staticcheck // This test verifies the documented nil rejection.
	if err := runtime.Run(nil); !errors.Is(err, serverhttp.ErrInvalidConfig) {
		t.Fatalf("Run(nil) error = %v, want ErrInvalidConfig", err)
	}
}

func TestRunForceClosesAfterShutdownTimeout(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	runtime, err := serverhttp.New(
		listener,
		http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
			close(requestEntered)
			<-releaseRequest
		}),
		serverhttp.WithShutdownTimeout(time.Nanosecond),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(ctx) }()
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		requestResult <- requestErr
	}()
	receiveTestValue(t, requestEntered)
	cancel()
	err = receiveTestValue(t, runResult)
	var runError *serverhttp.RunError
	if !errors.As(err, &runError) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run() error = %v, want deadline RunError", err)
	}
	if runError.Error() == "" {
		t.Fatal("RunError.Error() is blank")
	}
	close(releaseRequest)
	receiveTestValue(t, requestResult)
}

func TestRunTreatsExplicitServerCloseAsNormal(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	requestEntered := make(chan struct{})
	releaseRequest := make(chan struct{})
	runtime, err := serverhttp.New(listener, http.HandlerFunc(func(
		http.ResponseWriter,
		*http.Request,
	) {
		close(requestEntered)
		<-releaseRequest
	}))
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(context.Background()) }()
	requestResult := make(chan error, 1)
	go func() {
		response, requestErr := http.Get("http://" + listener.Addr().String())
		if requestErr == nil {
			requestErr = response.Body.Close()
		}
		requestResult <- requestErr
	}()
	receiveTestValue(t, requestEntered)
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := receiveTestValue(t, runResult); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	close(releaseRequest)
	receiveTestValue(t, requestResult)
}

type closeErrorListener struct {
	net.Listener
	err     error
	entered chan struct{}
	once    sync.Once
}

func (listener *closeErrorListener) Accept() (net.Conn, error) {
	listener.once.Do(func() { close(listener.entered) })

	return listener.Listener.Accept()
}

func (listener *closeErrorListener) Close() error {
	_ = listener.Listener.Close()

	return listener.err
}

func TestRunPreservesListenerCloseFailure(t *testing.T) {
	t.Parallel()

	underlying, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	closeFailure := errors.New("listener close failed")
	acceptEntered := make(chan struct{})
	listener := &closeErrorListener{
		Listener: underlying,
		err:      closeFailure,
		entered:  acceptEntered,
	}
	runtime, err := serverhttp.New(
		listener,
		http.NotFoundHandler(),
		serverhttp.WithShutdownTimeout(time.Second),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() { runResult <- runtime.Run(ctx) }()
	receiveTestValue(t, acceptEntered)
	cancel()
	err = receiveTestValue(t, runResult)
	if !errors.Is(err, closeFailure) {
		t.Fatalf("Run() error = %v, want close failure", err)
	}
}
