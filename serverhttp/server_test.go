package serverhttp_test

import (
	"context"
	"errors"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/faustbrian/golib/pkg/service/serverhttp"
)

func TestNewAppliesSecureDefaultsWithoutStartingWork(t *testing.T) {
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
	server := runtime.HTTPServer()
	if server.Handler == nil {
		t.Fatal("HTTPServer().Handler = nil")
	}
	if server.ReadTimeout != 30*time.Second {
		t.Fatalf("ReadTimeout = %v, want 30s", server.ReadTimeout)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Fatalf("ReadHeaderTimeout = %v, want 5s", server.ReadHeaderTimeout)
	}
	if server.WriteTimeout != 30*time.Second {
		t.Fatalf("WriteTimeout = %v, want 30s", server.WriteTimeout)
	}
	if server.IdleTimeout != 2*time.Minute {
		t.Fatalf("IdleTimeout = %v, want 2m", server.IdleTimeout)
	}
	if server.MaxHeaderBytes != 1<<20 {
		t.Fatalf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, 1<<20)
	}
}

func TestNewRejectsInvalidServerOptions(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })

	tests := map[string]struct {
		listener net.Listener
		option   serverhttp.Option
	}{
		"nil listener":      {option: serverhttp.WithReadTimeout(time.Second)},
		"nil option":        {listener: listener},
		"negative read":     {listener: listener, option: serverhttp.WithReadTimeout(-1)},
		"negative header":   {listener: listener, option: serverhttp.WithReadHeaderTimeout(-1)},
		"negative write":    {listener: listener, option: serverhttp.WithWriteTimeout(-1)},
		"negative idle":     {listener: listener, option: serverhttp.WithIdleTimeout(-1)},
		"negative shutdown": {listener: listener, option: serverhttp.WithShutdownTimeout(-1)},
		"zero shutdown":     {listener: listener, option: serverhttp.WithShutdownTimeout(0)},
		"negative body":     {listener: listener, option: serverhttp.WithBodyLimit(-1)},
		"invalid headers":   {listener: listener, option: serverhttp.WithMaxHeaderBytes(0)},
		"invalid request ID": {
			listener: listener,
			option: serverhttp.WithRequestIDs(serverhttp.RequestIDConfig{
				Header: "bad header",
			}),
		},
		"nil middleware": {
			listener: listener,
			option:   serverhttp.WithMiddleware(nil),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			options := []serverhttp.Option{test.option}
			if name == "nil listener" {
				options = []serverhttp.Option{test.option}
			}
			_, err := serverhttp.New(test.listener, http.NotFoundHandler(), options...)
			if !errors.Is(err, serverhttp.ErrInvalidConfig) {
				t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestNewRejectsMiddlewareReturningNil(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer func() { _ = listener.Close() }()
	returnsNil := serverhttp.Middleware(func(http.Handler) http.Handler { return nil })

	runtime, err := serverhttp.New(
		listener,
		http.NotFoundHandler(),
		serverhttp.WithMiddleware(returnsNil),
	)
	if runtime != nil {
		t.Fatalf("New() runtime = %v, want nil", runtime)
	}
	if !errors.Is(err, serverhttp.ErrInvalidConfig) {
		t.Fatalf("New() error = %v, want ErrInvalidConfig", err)
	}
}

func TestServerErrorContracts(t *testing.T) {
	t.Parallel()

	configError := &serverhttp.ConfigError{Field: "field", Reason: "reason"}
	if configError.Error() == "" {
		t.Fatal("ConfigError.Error() is blank")
	}
	stateError := &serverhttp.StateError{Operation: "run", State: "used"}
	if stateError.Error() == "" || !errors.Is(stateError, serverhttp.ErrInvalidState) {
		t.Fatalf("StateError = %v", stateError)
	}
	serveFailure := errors.New("serve failed")
	serveError := &serverhttp.ServeError{Err: serveFailure}
	if serveError.Error() == "" || !errors.Is(serveError, serveFailure) {
		t.Fatalf("ServeError = %v", serveError)
	}
}

func TestCloseBeforeRunReleasesOwnedListener(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	runtime, err := serverhttp.New(listener, http.NotFoundHandler())
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	connection, err := net.DialTimeout("tcp", address, 100*time.Millisecond)
	if err == nil {
		_ = connection.Close()
		t.Fatal("listener still accepted connections after Close")
	}
	if err := runtime.Run(context.Background()); !errors.Is(err, serverhttp.ErrInvalidState) {
		t.Fatalf("Run() after Close error = %v, want ErrInvalidState", err)
	}
}

func TestNewAppliesEveryExplicitServerOption(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	runtime, err := serverhttp.New(
		listener,
		http.NotFoundHandler(),
		serverhttp.WithReadTimeout(0),
		serverhttp.WithReadHeaderTimeout(time.Second),
		serverhttp.WithWriteTimeout(2*time.Second),
		serverhttp.WithIdleTimeout(3*time.Second),
		serverhttp.WithMaxHeaderBytes(4096),
	)
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	server := runtime.HTTPServer()
	if server.ReadTimeout != 0 ||
		server.ReadHeaderTimeout != time.Second ||
		server.WriteTimeout != 2*time.Second ||
		server.IdleTimeout != 3*time.Second ||
		server.MaxHeaderBytes != 4096 {
		t.Fatalf("HTTP server options = %#v", server)
	}
}
