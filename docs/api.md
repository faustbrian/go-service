# API reference

Every exported symbol is documented in source and rendered by `go doc` and
pkg.go.dev. This page is the package-selection and contract index; it avoids
copying signatures that could drift from the compiler-checked source.

## `service`

High-level construction: `Definition`, `Commands`, `Command`,
`CommandFor`, `CommandSpec`, `CommandKind`, `Invocation`, `BuildContext`,
`Plan`, `Task`, `HTTP`, `Management`, `ReadinessCheck`, `Identity`, and
`ProcessIdentity`. Optional runtime facilities are `RuntimeObserver`,
`RuntimeObserverFunc`, `RuntimeEvent`, `RuntimeEventKind`,
`RuntimeEventResult`, `Maintenance`, `MaintenanceState`, `MaintenanceSource`,
`MaintenanceStore`, `MaintenanceStoreOperations`, `FileMaintenanceStore`,
`NewFileMaintenanceStore`, and `NewSharedMaintenanceStore`.

High-level execution: `Main` supplies process state and returns an exit code;
`Execute` is the deterministic in-process boundary.
`CommandSpec.Options` registers typed `cli.OptionDefinition` values before the
configuration loader receives the immutable raw invocation.

Low-level construction and state: `Config`, `Component`, `New`, `Service`,
`State`, and the `StateNew`, `StateStarting`, `StateReady`, `StateDraining`,
`StateStopping`, `StateStopped`, and `MaxRuntimeIdentityBytes` constants.

Operations: `Start`, `Ready`, `Drain`, `Go`, `Context`, `Shutdown`, `Run`,
`RunWithSignals`, `Wait`, and `WaitWithSignals`. `Run` starts the service;
`Wait` requires an already-started service and supports tasks registered with
`Go`. `Config.StartupTimeout` bounds component acquisition,
`Config.RollbackTimeout` bounds partial-start cleanup, and `Config.MaxTasks`
provides a defaulted hard bound for active supervision. After cancellation, a
task may return either its context error or cancellation cause without turning
graceful shutdown into a task failure.
Component, task, command, service, and readiness names that can enter runtime
observations are limited to `MaxRuntimeIdentityBytes` bytes.

`Component.CloseAdmission` is the optional synchronous drain boundary. It runs
once before service-initiated cancellation and before `Stop`; a parent context
may already be canceled. `Stop` remains the bounded, context-aware active-work
drain and shutdown boundary.

Errors: `ErrInvalidDefinition`, `ErrInvalidConfig`, `ErrInvalidState`,
`ErrShutdown`, `ErrSignal`, `DefinitionError`, `ConfigurationError`,
`ConstructionError`, `ConfigError`, `StateError`, `ComponentError`,
`PanicError`, `StartupError`, `ShutdownError`, `ShutdownTimeoutError`, and
`SignalError`, plus `ErrMaintenance` and `MaintenanceError`. Typed aggregate
errors implement multi-`Unwrap`, so `errors.Is`
and `errors.As` inspect every retained cause.

## `serverhttp`

Runtime: `New`, `Server`, `HTTPServer`, `Run`, `Close`, and configuration options
`WithReadTimeout`, `WithReadHeaderTimeout`, `WithWriteTimeout`,
`WithIdleTimeout`, `WithShutdownTimeout`, `WithMaxHeaderBytes`,
`WithBodyLimit`, `WithBaseContext`, `WithConnContext`, `WithCorrelation`,
`WithIngressMiddleware`, and `WithMiddleware`.

Middleware: `Middleware`, `Chain`, `Recover`, and `LimitBody`. Correlation
identity is owned by `correlation/http`.

Errors: `ErrInvalidConfig`, `ErrInvalidState`, `ConfigError`, `StateError`,
`ServeError`, and `RunError`.

## `healthhttp`

Construction: `Config`, `New`, `Probes`, and the `Liveness`, `Startup`, and
`Readiness` handlers. Lifecycle input is the two-method `StateSource` contract.
`Observer`, `ObserverFunc`, and `Observation` expose bounded probe results.

Checks: `Check`, `CheckFunc`, `StateSource`, `Mode`, `ModeConcurrent`, `ModeSequential`,
`CheckResult`, and `Response`. Configuration controls per-check timeout,
concurrency, maximum check count, and secret-safe details.

Errors: `ErrInvalidConfig` and `ConfigError`.

## `integration`

`Hook`, `Hooks`, and `New` adapt caller-owned startup, admission closure, and
shutdown operations to a
`service.Component`. `WithSlog` accepts a caller-owned logger and bounded
attributes. `ErrInvalidConfig` and `ConfigError` describe rejected options.

## `servicetest`

Synchronization: zero-safe `Barrier` with `Entered`, `Wait`, and `Release`.

Fixtures: `ComponentConfig`, `NewComponent`, zero-safe `Recorder`, `Record`,
and `Events`.

HTTP: `Probe`, `ProbeResult`, `ErrInvalidConfig`, and `ConfigError`.

Run `go doc -all github.com/faustbrian/golib/pkg/service` and substitute any
subpackage name for compiler-matched signatures and field documentation.
