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

The same adapter can call explicit registration hooks for `go-telemetry`,
`go-scheduler`, or `go-queue`. Hook errors remain intact for `errors.Is` and
`errors.As`. Cleanup runs only when the hook component started successfully and
follows normal reverse service order.

## Logging

`WithSlog` accepts a caller-owned `*slog.Logger`, including a logger created by
`go-log`. It records only component name and supplied bounded attributes. Hook
error values are deliberately not logged because configuration and provider
errors may contain secrets. The logger and its handler are never closed or
replaced.

## Telemetry

Construct OpenTelemetry or `go-telemetry` providers before runtime startup and
pass them directly to application code. Use a hook only for an explicit
caller-owned registration step. `go-service` does not create or shut down SDK
providers, exporters, readers, processors, or collectors. Provider shutdown
belongs in the application composition root where its ordering and deadline
are visible.
