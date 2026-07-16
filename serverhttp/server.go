// Package serverhttp provides an owned standard-library HTTP server runtime.
package serverhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	defaultReadTimeout       = 30 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultShutdownTimeout   = 30 * time.Second
	defaultBodyLimit         = int64(1 << 20)
	defaultMaxHeaderBytes    = 1 << 20
)

// ErrInvalidConfig identifies invalid HTTP runtime configuration.
var ErrInvalidConfig = errors.New("invalid HTTP server configuration")

// ErrInvalidState identifies an HTTP runtime operation rejected by its state.
var ErrInvalidState = errors.New("invalid HTTP server state")

// ConfigError identifies one invalid HTTP runtime field.
type ConfigError struct {
	// Field identifies the rejected configuration path.
	Field string
	// Reason describes why Field was rejected.
	Reason string
}

// StateError reports an invalid HTTP runtime operation.
type StateError struct {
	// Operation is the rejected runtime operation.
	Operation string
	// State describes the runtime state in which Operation was rejected.
	State string
}

// Error implements error.
func (err *StateError) Error() string {
	return fmt.Sprintf("cannot %s HTTP server in %s state: %v",
		err.Operation, err.State, ErrInvalidState)
}

// Unwrap makes StateError inspectable with errors.Is.
func (err *StateError) Unwrap() error {
	return ErrInvalidState
}

// ServeError reports an unexpected listener or serving failure.
type ServeError struct {
	// Err is the unexpected listener or serving failure.
	Err error
}

// Error implements error.
func (err *ServeError) Error() string {
	return fmt.Sprintf("serve HTTP: %v", err.Err)
}

// Unwrap returns the serving failure.
func (err *ServeError) Unwrap() error {
	return err.Err
}

// RunError aggregates failures observed while stopping an HTTP server.
type RunError struct {
	// Failures contains graceful-shutdown and forced-close failures.
	Failures []error
}

// Error implements error.
func (err *RunError) Error() string {
	return fmt.Sprintf("stop HTTP server failed with %d error(s)", len(err.Failures))
}

// Unwrap exposes every runtime failure.
func (err *RunError) Unwrap() []error {
	return err.Failures
}

// Error implements error.
func (err *ConfigError) Error() string {
	return fmt.Sprintf("%s: %s: %v", err.Field, err.Reason, ErrInvalidConfig)
}

// Unwrap makes ConfigError inspectable with errors.Is.
func (err *ConfigError) Unwrap() error {
	return ErrInvalidConfig
}

type config struct {
	readTimeout       time.Duration
	readHeaderTimeout time.Duration
	writeTimeout      time.Duration
	idleTimeout       time.Duration
	shutdownTimeout   time.Duration
	bodyLimit         int64
	maxHeaderBytes    int
	requestIDs        RequestIDConfig
	middleware        []Middleware
}

func defaultConfig() config {
	return config{
		readTimeout:       defaultReadTimeout,
		readHeaderTimeout: defaultReadHeaderTimeout,
		writeTimeout:      defaultWriteTimeout,
		idleTimeout:       defaultIdleTimeout,
		shutdownTimeout:   defaultShutdownTimeout,
		bodyLimit:         defaultBodyLimit,
		maxHeaderBytes:    defaultMaxHeaderBytes,
		requestIDs: RequestIDConfig{
			Header:    defaultRequestIDHeader,
			MaxLength: defaultRequestIDMaxLength,
			Generator: randomRequestID,
		},
	}
}

// Option configures a Server. A nil Option is invalid.
type Option func(*config) error

// WithReadTimeout configures the full request read timeout. Zero disables it.
func WithReadTimeout(timeout time.Duration) Option {
	return durationOption("ReadTimeout", timeout, func(config *config) {
		config.readTimeout = timeout
	})
}

// WithReadHeaderTimeout configures the request-header read timeout. Zero
// disables it.
func WithReadHeaderTimeout(timeout time.Duration) Option {
	return durationOption("ReadHeaderTimeout", timeout, func(config *config) {
		config.readHeaderTimeout = timeout
	})
}

// WithWriteTimeout configures the response write timeout. Zero disables it.
func WithWriteTimeout(timeout time.Duration) Option {
	return durationOption("WriteTimeout", timeout, func(config *config) {
		config.writeTimeout = timeout
	})
}

// WithIdleTimeout configures the keep-alive idle timeout. Zero disables it.
func WithIdleTimeout(timeout time.Duration) Option {
	return durationOption("IdleTimeout", timeout, func(config *config) {
		config.idleTimeout = timeout
	})
}

// WithShutdownTimeout configures the graceful-shutdown bound. Zero is invalid
// because Run always owns a finite shutdown bound.
func WithShutdownTimeout(timeout time.Duration) Option {
	return func(config *config) error {
		if timeout <= 0 {
			return &ConfigError{
				Field:  "ShutdownTimeout",
				Reason: "must be positive",
			}
		}
		config.shutdownTimeout = timeout

		return nil
	}
}

// WithBodyLimit configures the maximum request body size. Zero disables the
// limit.
func WithBodyLimit(limit int64) Option {
	return func(config *config) error {
		if limit < 0 {
			return &ConfigError{Field: "BodyLimit", Reason: "must not be negative"}
		}
		config.bodyLimit = limit

		return nil
	}
}

// WithMaxHeaderBytes configures the maximum request-header size.
func WithMaxHeaderBytes(limit int) Option {
	return func(config *config) error {
		if limit <= 0 {
			return &ConfigError{Field: "MaxHeaderBytes", Reason: "must be positive"}
		}
		config.maxHeaderBytes = limit

		return nil
	}
}

// WithRequestIDs configures request ID generation and inbound trust.
func WithRequestIDs(requestIDs RequestIDConfig) Option {
	return func(config *config) error {
		configured, err := normalizeRequestIDConfig(requestIDs)
		if err != nil {
			return err
		}
		config.requestIDs = configured

		return nil
	}
}

// WithMiddleware appends user middleware in visible listed order.
func WithMiddleware(middleware ...Middleware) Option {
	return func(config *config) error {
		for _, item := range middleware {
			if item == nil {
				return &ConfigError{Field: "middleware", Reason: "must not contain nil"}
			}
		}
		config.middleware = append(config.middleware, middleware...)

		return nil
	}
}

// Server owns one listener and its standard-library HTTP server.
type Server struct {
	mu              sync.Mutex
	ran             bool
	listener        net.Listener
	httpServer      *http.Server
	shutdownTimeout time.Duration
	close           func() error
	closeOnce       sync.Once
	closeErr        error
}

// New constructs a server without accepting connections or starting
// goroutines. Ownership of listener transfers to Server after success.
func New(
	listener net.Listener,
	handler http.Handler,
	options ...Option,
) (*Server, error) {
	if listener == nil {
		return nil, &ConfigError{Field: "listener", Reason: "must not be nil"}
	}
	configured := defaultConfig()
	for index, option := range options {
		if option == nil {
			return nil, &ConfigError{
				Field:  fmt.Sprintf("options[%d]", index),
				Reason: "must not be nil",
			}
		}
		if err := option(&configured); err != nil {
			return nil, err
		}
	}
	if handler == nil {
		handler = http.NotFoundHandler()
	}
	stack := []Middleware{
		Recover(),
		requestIDs(configured.requestIDs),
		limitBody(configured.bodyLimit),
	}
	stack = append(stack, configured.middleware...)
	for index := len(stack) - 1; index >= 0; index-- {
		handler = stack[index](handler)
		if handler == nil {
			return nil, &ConfigError{
				Field:  "middleware",
				Reason: "must not return nil",
			}
		}
	}

	server := &Server{
		listener: listener,
		httpServer: &http.Server{
			Handler:           handler,
			ReadTimeout:       configured.readTimeout,
			ReadHeaderTimeout: configured.readHeaderTimeout,
			WriteTimeout:      configured.writeTimeout,
			IdleTimeout:       configured.idleTimeout,
			MaxHeaderBytes:    configured.maxHeaderBytes,
		},
		shutdownTimeout: configured.shutdownTimeout,
	}
	server.close = server.httpServer.Close

	return server, nil
}

// HTTPServer returns the configured server. Callers must not mutate it after
// Run begins.
func (server *Server) HTTPServer() *http.Server {
	return server.httpServer
}

// Close releases the owned listener before Run or force-closes an active
// server. Concurrent and repeated calls return the same result. A closed server
// cannot be run.
func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		server.mu.Lock()
		ran := server.ran
		if !ran {
			server.ran = true
		}
		server.mu.Unlock()

		var failures []error
		if ran {
			if err := server.close(); err != nil && !errors.Is(err, net.ErrClosed) {
				failures = append(failures, err)
			}
		}
		if err := server.listener.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			failures = append(failures, err)
		}
		server.closeErr = errors.Join(failures...)
	})

	return server.closeErr
}

// Run serves until ctx is canceled or serving fails. It owns and joins the
// Serve goroutine before returning and may be called only once.
func (server *Server) Run(ctx context.Context) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}

	server.mu.Lock()
	if server.ran {
		server.mu.Unlock()

		return &StateError{Operation: "run", State: "used"}
	}
	server.ran = true
	if server.httpServer.BaseContext == nil {
		server.httpServer.BaseContext = func(net.Listener) context.Context {
			return ctx
		}
	}
	server.mu.Unlock()

	serveResult := make(chan error, 1)
	go func() {
		serveResult <- server.httpServer.Serve(server.listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return &ServeError{Err: err}
	case <-ctx.Done():
	}

	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		server.shutdownTimeout,
	)
	defer cancel()

	var failures []error
	if err := server.httpServer.Shutdown(shutdownContext); err != nil {
		failures = append(failures, err)
		if closeErr := server.Close(); closeErr != nil {
			failures = append(failures, closeErr)
		}
	}
	<-serveResult
	if len(failures) > 0 {
		return &RunError{Failures: failures}
	}

	return nil
}

func durationOption(
	field string,
	timeout time.Duration,
	apply func(*config),
) Option {
	return func(config *config) error {
		if timeout < 0 {
			return &ConfigError{Field: field, Reason: "must not be negative"}
		}
		apply(config)

		return nil
	}
}
