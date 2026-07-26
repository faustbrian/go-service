# Integration hooks

`integration` converts caller-owned start and stop functions into an ordinary
ordered `service.Component`. It does not import configuration, logging, or
telemetry SDKs and does not create providers, handlers, exporters, collectors,
clients, queues, or schedulers.

Place prerequisites first so a failure prevents partial startup:

```go
configuration, err := integration.New("configuration", integration.Hooks{
    Start: func(ctx context.Context) error {
        loaded, err := loader.Load(ctx)
        if err != nil {
            return err
        }
        config = loaded
        return nil
    },
})
runtime, err := service.New(service.Config{
    Components: []service.Component{configuration, worker},
})
```

The same adapter can call explicit registration hooks for `telemetry`,
`scheduler`, or `queue`. Register queue ownership before scheduler
ownership so reverse shutdown drains scheduled publication before releasing
the queue. Run blocking scheduler loops through `Service.Go`; their context is
canceled before component cleanup begins. A runner may return `ctx.Err()` or
`context.Cause(ctx)` after observing cancellation; both are normal supervised
shutdown results. Every hook must honor its context, including any queue release
or scheduler drain operation. Hook errors remain intact for `errors.Is` and
`errors.As`. Cleanup runs only when the hook component started successfully and
follows normal reverse service order.

## Logging

`WithSlog` accepts a caller-owned `*slog.Logger`, including a logger created by
`log`. It records only component name and supplied bounded attributes. Hook
error values are deliberately not logged because configuration and provider
errors may contain secrets. The logger and its handler are never closed or
replaced. Configure `WithSlog` at most once for a component; duplicates are
rejected instead of silently replacing earlier attributes or logger ownership.

## Telemetry

Construct OpenTelemetry or `telemetry` providers before runtime startup and
pass them directly to application code. Use a hook only for an explicit
caller-owned registration step. `service` does not create or shut down SDK
providers, exporters, readers, processors, or collectors. Provider shutdown
belongs in the application composition root where its ordering and deadline
are visible.

The executable `ExampleWithSlog` demonstrates a caller-owned logger and an
explicit provider-registration hook. `ExampleNew` demonstrates configuration
loading. `ExampleNew_queueAndScheduler` proves queue-before-scheduler startup,
supervised scheduler work, and scheduler-before-queue cleanup. These examples
are executed by `go test` and therefore by the docs and complete quality gates.

Trace propagation, metric names, labels, and cardinality policy remain owned by
the application and its selected telemetry integration. Hooks receive the
caller's context unchanged, but `service` does not install propagators or
derive telemetry attributes from component names, check names, or request IDs.

## Real-module compatibility

The isolated `compatibility` module executes the same contracts against pinned
revisions of `config`, `log`, `telemetry`, `authentication`,
`authorization`, `scheduler`, and `queue`. It proves actual middleware
ordering, disabled telemetry startup and shutdown, configuration loading,
queue ownership, scheduler draining, cancellation-result handling, and logger
compatibility. It also proves that duplicate global telemetry registration
fails startup before later components and that rollback releases the first
registration. A real sensitive `config` source failure additionally proves
that cause identity survives, its text is redacted, and no later component
starts. Its separate `go.mod` keeps all optional dependencies out of the core
module graph.

Run `make integration-compatibility` with the current stable Go release. The
separate optional-integration workflow runs the race detector and reachable
vulnerability scan for that pinned graph on pushes, pull requests, merge
queues, and a weekly schedule. The core module remains compatible with its Go
1.25 minimum; the compatibility module follows the newest minimum required by
the integrated modules.
