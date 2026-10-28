# API reference

[Documentation map](index.md) · [Repository README](../README.md)
[Next: Adoption examples](adoption.md)

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
configuration loader receives invocation-local typed input. In `Load`, use
`option.Get(invocation.Input())` and `option.State(invocation.Input())` with
the original declared binding; do not parse `Invocation.Args` again. Defaults,
explicit-empty values and omission retain the CLI owner's semantics. A zero
`Invocation` has no parsed values. Built-in collection reads are copied;
arbitrary custom reference values retain the ownership rules of `cli.Input`.
See the compiling [`Invocation.Input` example](../lifecycle_example_test.go).

Low-level construction and state: `Config`, `Component`, `New`, `Service`,
`State`, and the `StateNew`, `StateStarting`, `StateReady`, `StateDraining`,
`StateStopping`, `StateStopped`, and `MaxRuntimeIdentityBytes` constants.

Operations: `Start`, `Ready`, `Drain`, `Go`, `Context`, `Shutdown`, `Run`,
`RunWithSignals`, `Wait`, and `WaitWithSignals`. `Run` starts the service;
`Wait` requires an already-started service and supports tasks registered with
`Go`. `Config.StartupTimeout` supplies a cooperative component deadline,
`Config.RollbackTimeout` supplies the partial-start cleanup deadline, and
`Config.MaxTasks` provides a defaulted hard bound for active supervision. After
cancellation, a task may return its context error or cancellation cause without turning
graceful shutdown into a task failure.
Component, task, command, service, and readiness names that can enter runtime
observations are limited to `MaxRuntimeIdentityBytes` bytes.

`Component.CloseAdmission` is the optional synchronous drain boundary. It runs
once before service-initiated cancellation and before `Stop`; a parent context
may already be canceled. `Stop` receives the first cleanup caller's context
after supervised tasks join. A waiter timeout does not terminate callbacks; cleanup remains joinable. Admission
closure has no timeout and must not reenter drain/shutdown.

Errors: `ErrInvalidDefinition`, `ErrInvalidConfig`, `ErrInvalidState`,
`ErrShutdown`, `ErrSignal`, `DefinitionError`, `ConfigurationError`,
`ConstructionError`, `ConfigError`, `StateError`, `ComponentError`,
`PanicError`, `ErrorGraphError`, `StartupError`, `ShutdownError`, `ShutdownTimeoutError`, and
`SignalError`, plus `ErrMaintenance` and `MaintenanceError`. Typed aggregate
errors implement multi-`Unwrap`, so `errors.Is`
and `errors.As` inspect every retained cause.
`ErrorGraphError` preserves a cyclic callback error in
`Value` without automatically unwrapping an unsafe graph.

## `serverhttp`

Runtime: `New`, `Server`, `HTTPServer`, `Run`, `Close`, `CloseAdmission`,
`WaitHandlers`, and configuration options
`WithReadTimeout`, `WithReadHeaderTimeout`, `WithWriteTimeout`,
`WithIdleTimeout`, `WithShutdownTimeout`, `WithMaxHeaderBytes`,
`WithMaxConnections`,
`WithBodyLimit`, `WithPanicRecovery`, `WithHandlerJoin`, `WithBaseContext`, `WithConnContext`,
`WithCorrelation`, `WithIngressMiddleware`, and `WithMiddleware`.

Middleware: `Middleware`, `Chain`, `Recover`, and `LimitBody`. Correlation
identity is owned by `correlation/adapters/http`.

Errors: `ErrInvalidConfig`, `ErrInvalidState`, `ErrHandlersUnjoined`, `ConfigError`, `StateError`,
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

Run `go doc -all github.com/faustbrian/go-service/v3` and substitute any
subpackage name for compiler-matched signatures and field documentation.
