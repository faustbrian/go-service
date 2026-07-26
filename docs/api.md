# API reference

Every exported symbol is documented in source and rendered by `go doc` and
pkg.go.dev. This page is the package-selection and contract index; it avoids
copying signatures that could drift from the compiler-checked source.

## `service`

Construction and state: `Config`, `Component`, `New`, `Service`, `State`, and
the `StateNew`, `StateStarting`, `StateReady`, `StateDraining`,
`StateStopping`, and `StateStopped` constants.

Operations: `Start`, `Ready`, `Drain`, `Go`, `Context`, `Shutdown`, `Run`,
`RunWithSignals`, `Wait`, and `WaitWithSignals`. `Run` starts the service;
`Wait` requires an already-started service and supports tasks registered with
`Go`. `Config.MaxTasks` provides a defaulted hard bound for active supervision.
After cancellation, a task may return either its context error or cancellation
cause without turning graceful shutdown into a task failure.

Errors: `ErrInvalidConfig`, `ErrInvalidState`, `ErrShutdown`, `ErrSignal`,
`ConfigError`, `StateError`, `ComponentError`, `PanicError`, `StartupError`,
`ShutdownError`, and `SignalError`. Typed aggregate errors implement multi-
`Unwrap`, so `errors.Is` and `errors.As` inspect every retained cause.

## `serverhttp`

Runtime: `New`, `Server`, `HTTPServer`, `Run`, `Close`, and configuration options
`WithReadTimeout`, `WithReadHeaderTimeout`, `WithWriteTimeout`,
`WithIdleTimeout`, `WithShutdownTimeout`, `WithMaxHeaderBytes`,
`WithBodyLimit`, `WithRequestIDs`, and `WithMiddleware`.

Middleware: `Middleware`, `Chain`, `Recover`, `LimitBody`, `RequestIDs`,
`RequestIDConfig`, `RequestIDGenerator`, and `RequestID`.

Errors: `ErrInvalidConfig`, `ErrInvalidState`, `ConfigError`, `StateError`,
`ServeError`, and `RunError`.

## `healthhttp`

Construction: `Config`, `New`, `Probes`, and the `Liveness`, `Startup`, and
`Readiness` handlers. Lifecycle input is the one-method `StateSource` contract.

Checks: `Check`, `CheckFunc`, `Mode`, `ModeConcurrent`, `ModeSequential`,
`CheckResult`, and `Response`. Configuration controls per-check timeout,
concurrency, maximum check count, and secret-safe details.

Errors: `ErrInvalidConfig` and `ConfigError`.

## `integration`

`Hook`, `Hooks`, and `New` adapt caller-owned operations to a
`service.Component`. `WithSlog` accepts a caller-owned logger and bounded
attributes. `ErrInvalidConfig` and `ConfigError` describe rejected options.

## `servicetest`

Synchronization: zero-safe `Barrier` with `Entered`, `Wait`, and `Release`.

Fixtures: `ComponentConfig`, `NewComponent`, zero-safe `Recorder`, `Record`,
and `Events`.

HTTP: `Probe`, `ProbeResult`, `ErrInvalidConfig`, and `ConfigError`.

Run `go doc -all github.com/faustbrian/golib/pkg/service/service` and substitute any
subpackage name for compiler-matched signatures and field documentation.
