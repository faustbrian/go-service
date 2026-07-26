# service

`service` is a standard-library-first runtime foundation for independently
deployed Go services. It coordinates lifecycle, HTTP serving, probes, and
cross-cutting hooks without choosing an application architecture, router,
logger backend, telemetry SDK, queue, database, or configuration source.

The `v1` API is stable and follows semantic versioning.

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
[hardening evidence](docs/hardening.md). The
[release evidence matrix](docs/evidence.md) maps every material promise to
implementation, tests, and public contracts.

## Package surface

| Package | Responsibility |
| --- | --- |
| `service` | Lifecycle, signals, supervision, and ordered cleanup |
| `serverhttp` | Secure HTTP defaults, serving, draining, and middleware |
| `healthhttp` | Startup, liveness, readiness, and dependency checks |
| `integration` | Dependency-neutral hooks for caller-owned facilities |
| `servicetest` | Deterministic lifecycle and probe test utilities |

## Five-minute lifecycle

```go
package main

import (
    "context"
    "log"

    "github.com/faustbrian/golib/pkg/service/service"
)

func main() {
    runtime, err := service.New(service.Config{})
    if err != nil {
        log.Fatal(err)
    }
    if err := runtime.Start(context.Background()); err != nil {
        log.Fatal(err)
    }
    if err := runtime.Go("worker", func(ctx context.Context) error {
        <-ctx.Done()

        return nil
    }); err != nil {
        log.Fatal(err)
    }
    if err := service.Wait(
        context.Background(), runtime, service.RunConfig{},
    ); err != nil {
        log.Fatal(err)
    }
}
```

Save this as `main.go`, run `go mod init example`, add the module with
`go get github.com/faustbrian/golib/pkg/service`, and run it with `go run .`. Send
SIGINT or SIGTERM to stop it. Startup follows registration order. Failed
startup rolls back only components that successfully started. Shutdown cancels
the service context, drains readiness, stops components in reverse order, and
joins tasks started with `Service.Go`.

## Compatibility

The `v1` line supports Go 1.25 and the current stable Go release. CI verifies
both versions on Linux, macOS, and Windows. The public API and documented
response contracts follow semantic versioning.

## License

MIT. See [LICENSE](LICENSE).
