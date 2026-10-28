// Package serverhttp provides an owned standard-library HTTP server runtime.
// Start with New using a listener and caller-owned handler, then Run once or
// Close if construction is abandoned. Routing, TLS, authentication and hijacked
// connections remain caller-owned. Request/IO deadlines do not forcibly stop
// arbitrary handlers or callbacks; cancellation must be cooperative.
package serverhttp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/faustbrian/go-correlation"
	httpcorrelation "github.com/faustbrian/go-correlation/adapters/http"
)

const (
	defaultReadTimeout       = 30 * time.Second
	defaultReadHeaderTimeout = 5 * time.Second
	defaultWriteTimeout      = 30 * time.Second
	defaultIdleTimeout       = 2 * time.Minute
	defaultShutdownTimeout   = 30 * time.Second
	defaultBodyLimit         = int64(1_048_576)
	defaultMaxHeaderBytes    = 1 << 20
)

// ErrInvalidConfig identifies invalid HTTP runtime configuration.
var ErrInvalidConfig = errors.New("invalid HTTP server configuration")

// ErrInvalidState identifies an HTTP runtime operation rejected by its state.
var ErrInvalidState = errors.New("invalid HTTP server state")

// ErrHandlersUnjoined identifies a waiter that left before all admitted
// handlers returned. It does not mean that handler ownership was released.
var ErrHandlersUnjoined = errors.New("HTTP handlers remain unjoined")

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
	// Failures contains serving, graceful-shutdown and forced-close failures.
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
	maxConnections    int
	panicRecovery     bool
	joinHandlers      bool
	baseContext       func(net.Listener) context.Context
	connContext       func(context.Context, net.Conn) context.Context
	correlation       *httpcorrelation.Middleware
	ingress           []Middleware
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
		panicRecovery:     true,
	}
}

// Option configures a Server. A nil Option is invalid.
type Option func(*config) error

// WithHandlerJoin enables full-stack handler ownership, disabled by default.
// CloseAdmission rejects later handlers and WaitHandlers joins admitted calls,
// including outer middleware finalizers after forced socket closure. Run still
// returns serving failures without waiting for handlers so a lifecycle owner
// can start its bounded cleanup. Callers must join before releasing dependencies.
// This does not detach request contexts or own hijacks/application-spawned work.
func WithHandlerJoin(enabled bool) Option {
	return func(config *config) error {
		config.joinHandlers = enabled
		return nil
	}
}

// WithPanicRecovery selects the built-in Recover middleware, enabled by default.
// False leaves request panics to caller middleware or net/http, which aborts
// the connection or HTTP/2 stream and may log panic values through ErrorLog.
// Direct invocation of HTTPServer().Handler then propagates uncaught panics.
// This does not change constructor callbacks, middleware order or other limits.
func WithPanicRecovery(enabled bool) Option {
	return func(config *config) error {
		config.panicRecovery = enabled

		return nil
	}
}

// WithMaxConnections bounds server-owned accepted connections, including idle
// sockets before request headers. Zero leaves admission unrestricted. A negative
// value is invalid. Saturation waits before Accept rather than creating an HTTP
// response or another accepted-socket queue. Closed connections release capacity;
// hijack transfers ownership and releases capacity to the caller, which must
// separately bound and close hijacked work. HTTP/2 stream admission is separate.
// Caller-supplied TLS connections retain their native identity and metadata.
func WithMaxConnections(maximum int) Option {
	return func(config *config) error {
		if maximum < 0 {
			return &ConfigError{Field: "MaxConnections", Reason: "must not be negative"}
		}
		config.maxConnections = maximum
		return nil
	}
}

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

// WithBaseContext sets the base context for accepted connections. The callback
// must return a non-nil context.
func WithBaseContext(base func(net.Listener) context.Context) Option {
	return func(config *config) error {
		if base == nil {
			return &ConfigError{Field: "BaseContext", Reason: "must not be nil"}
		}
		config.baseContext = base

		return nil
	}
}

// WithConnContext derives a context for each accepted connection. The callback
// must return a non-nil context.
func WithConnContext(connection func(context.Context, net.Conn) context.Context) Option {
	return func(config *config) error {
		if connection == nil {
			return &ConfigError{Field: "ConnContext", Reason: "must not be nil"}
		}
		config.connContext = connection

		return nil
	}
}

// WithCorrelation installs correlation-owned HTTP identity after optional panic
// recovery and before ingress middleware or body limits.
func WithCorrelation(
	factory *correlation.Factory,
	options httpcorrelation.Options,
) Option {
	return func(config *config) error {
		if config.correlation != nil {
			return &ConfigError{
				Field: "Correlation", Reason: "duplicates request identity ownership",
			}
		}
		middleware, err := httpcorrelation.New(factory, options)
		if err != nil {
			return err
		}
		config.correlation = middleware

		return nil
	}
}

// WithIngressMiddleware appends middleware after request identity and before
// request body limits.
func WithIngressMiddleware(middleware ...Middleware) Option {
	return func(config *config) error {
		for _, item := range middleware {
			if item == nil {
				return &ConfigError{
					Field: "ingress middleware", Reason: "must not contain nil",
				}
			}
		}
		config.ingress = append(config.ingress, middleware...)

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

// Server owns one listener and its standard-library HTTP server. Run is
// one-shot; Close is concurrency-safe and repeatable. Use New rather than the
// zero value, and do not copy after use. Handlers must support concurrent calls.
// Shutdown joins serving, not arbitrary hijacked connection/application work.
type Server struct {
	mu              sync.Mutex
	ran             bool
	listener        net.Listener
	httpServer      *http.Server
	shutdownTimeout time.Duration
	close           func() error
	closeOnce       sync.Once
	closeErr        error
	connections     *connectionLimitListener
	handlers        *handlerDrain
}

// New constructs a server without accepting connections or starting
// goroutines. Ownership of listener transfers to Server after success; failures
// leave it caller-owned. Nil handler uses NotFoundHandler. Options and middleware
// factories execute synchronously without panic containment; request-time
// recovery is a separate boundary. The composed order is optional recovery,
// correlation, ingress, body limit, then ordinary middleware and handler.
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
	handler = handlerOrNotFound(handler)
	var stack []Middleware
	if configured.panicRecovery {
		stack = append(stack, Recover())
	}
	switch configured.correlation {
	case nil:
	default:
		stack = append(stack, configured.correlation.Wrap)
	}
	stack = append(stack, configured.ingress...)
	stack = append(stack, limitBody(configured.bodyLimit))
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
	var handlers *handlerDrain
	if configured.joinHandlers {
		handlers = newHandlerDrain()
		handler = handlers.wrap(handler)
	}

	var connections *connectionLimitListener
	if configured.maxConnections > 0 {
		connections = limitConnections(listener, configured.maxConnections)
		listener = connections
	}

	server := &Server{
		listener:    listener,
		connections: connections,
		handlers:    handlers,
		httpServer: &http.Server{
			Handler:           handler,
			ReadTimeout:       configured.readTimeout,
			ReadHeaderTimeout: configured.readHeaderTimeout,
			WriteTimeout:      configured.writeTimeout,
			IdleTimeout:       configured.idleTimeout,
			MaxHeaderBytes:    configured.maxHeaderBytes,
			BaseContext:       configured.baseContext,
			ConnContext:       configured.connContext,
		},
		shutdownTimeout: configured.shutdownTimeout,
	}
	server.close = server.httpServer.Close

	return server, nil
}

// HTTPServer returns the configured server. Callers must not mutate it after
// Run begins. It is a borrowed pointer, not a snapshot; direct Serve, Close or
// mutation can bypass this wrapper's lifecycle/ownership contract.
func (server *Server) HTTPServer() *http.Server {
	return server.httpServer
}

// Close releases the owned listener before Run or force-closes an active
// server. Concurrent and repeated calls return the same result. A closed server
// cannot be run.
func (server *Server) Close() error {
	server.closeOnce.Do(func() {
		_ = server.CloseAdmission()
		server.mu.Lock()
		ran := server.ran
		if !ran {
			server.ran = true
		}
		server.mu.Unlock()

		var failures []error
		if ran {
			if err := server.close(); !errors.Is(err, net.ErrClosed) {
				failures = append(failures, err)
			}
		}
		if err := server.listener.Close(); !errors.Is(err, net.ErrClosed) {
			failures = append(failures, err)
		}
		server.closeErr = errors.Join(failures...)
	})

	return server.closeErr
}

// CloseAdmission rejects new handler invocations when WithHandlerJoin is
// enabled, without closing connections or waiting for admitted handlers. It is
// concurrency-safe and idempotent; without opt-in it is a no-op. Run completion,
// Close and WaitHandlers also close admission. It can be used before Run.
func (server *Server) CloseAdmission() error {
	if server.handlers != nil {
		server.handlers.closeAdmission()
	}
	return nil
}

// WaitHandlers closes admission and waits for every admitted ServeHTTP to
// return, including middleware finalizers. It requires WithHandlerJoin; otherwise
// it returns StateError. Nil ctx returns ConfigError. Completed joining wins over
// an already canceled context; incomplete joining returns ErrHandlersUnjoined
// together with context.Cause(ctx). A timed-out waiter never releases ownership:
// another concurrent or later waiter may establish completion. Callers own the
// wait budget and must not release dependencies on a failed wait. A handler
// cannot join itself until it returns; self-joins require a finite context.
func (server *Server) WaitHandlers(ctx context.Context) error {
	if ctx == nil {
		return &ConfigError{Field: "ctx", Reason: "must not be nil"}
	}
	if server.handlers == nil {
		return &StateError{Operation: "join handlers", State: "handler joining disabled"}
	}
	server.handlers.closeAdmission()
	select {
	case <-server.handlers.done:
		return nil
	default:
	}
	select {
	case <-server.handlers.done:
		return nil
	case <-ctx.Done():
		return errors.Join(ErrHandlersUnjoined, context.Cause(ctx))
	}
}

// Run serves until ctx is canceled or serving fails. It owns and joins the
// Serve goroutine before returning and may be called only once. Nil ctx is
// rejected. On cancellation it uses a fresh shutdown deadline, force-closes on
// graceful-shutdown failure and aggregates cleanup errors in RunError. Ordinary
// cancellation after successful cleanup returns nil. BaseContext defaults to
// ctx; a custom BaseContext controls request cancellation independently.
// Close can run concurrently, but net/http does not close hijacked connections
// or forcibly terminate an application handler.
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
	if server.connections != nil {
		observer := server.httpServer.ConnState
		server.httpServer.ConnState = func(connection net.Conn, state http.ConnState) {
			if state == http.StateClosed || state == http.StateHijacked {
				server.connections.release()
			}
			if observer != nil {
				observer(connection, state)
			}
		}
	}
	switch server.httpServer.BaseContext {
	case nil:
		server.httpServer.BaseContext = func(net.Listener) context.Context {
			return ctx
		}
	}
	server.mu.Unlock()
	defer func() { _ = server.CloseAdmission() }()

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
	if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
		failures = append(failures, &ServeError{Err: err})
	}
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
