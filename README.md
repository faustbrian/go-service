# go-service

`go-service` is a standard-library-first runtime foundation for independently
deployed Go services. It coordinates lifecycle, HTTP serving, probes, and
cross-cutting hooks without choosing an application architecture, router,
logger backend, telemetry SDK, queue, database, or configuration source.

The module is under active development and has not reached `v1`.

## Design

- Every goroutine has an owner, cancellation path, and join path.
- Startup is ordered; rollback and shutdown are reverse ordered and bounded.
- Lifecycle states and failure causes are explicit and observable.
- Each subpackage is independently importable and has no initialization side
  effects.
- Optional integrations accept caller-owned values and never own exporters,
  logging handlers, or configuration loading.

See [architecture](docs/architecture.md) and
[lifecycle and ownership](docs/lifecycle.md) for the complete contract.
The [adoption guides](docs/adoption.md) map each supported service shape to a
runnable program under `examples`.

Reference documentation includes the [API index](docs/api.md),
[Kubernetes operations](docs/kubernetes.md), [migration](docs/migration.md),
[security](docs/security.md), [performance](docs/performance.md), and current
[hardening evidence](docs/hardening.md).

## Planned package surface

| Package | Responsibility |
| --- | --- |
| `service` | Lifecycle, signals, supervision, and ordered cleanup |
| `serverhttp` | Secure HTTP defaults, serving, draining, and middleware |
| `healthhttp` | Startup, liveness, readiness, and dependency checks |
| `integration` | Dependency-neutral hooks for caller-owned facilities |
| `servicetest` | Deterministic lifecycle and probe test utilities |

## Five-minute lifecycle

```go
runtime, err := service.New(service.Config{
    Components: []service.Component{{
        Name:  "worker",
        Start: worker.Start,
        Stop:  worker.Stop,
    }},
})
if err != nil {
    return err
}
if err := runtime.Start(ctx); err != nil {
    return err
}
defer runtime.Shutdown(shutdownCtx)
```

Startup follows registration order. Failed startup rolls back only components
that successfully started. Shutdown cancels the service context, drains
readiness, stops components in reverse order, and joins tasks started with
`Service.Go`.

## Compatibility

The initial development line targets Go 1.25. No compatibility promise is made
until `v1.0.0`; after `v1`, the public API follows semantic versioning.

## License

MIT. See [LICENSE](LICENSE).
