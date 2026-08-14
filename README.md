# service

`service` is a standard-library-first runtime foundation for independently
deployed Go services. It coordinates lifecycle, HTTP serving, probes, and
cross-cutting hooks without choosing an application architecture, router,
logger backend, telemetry SDK, queue, database, or configuration source.

The cohesive API remains unreleased while the platform verification gates are
being completed.

## Design

- Every goroutine has an owner, cancellation path, and join path.
- Startup is ordered; rollback and shutdown are reverse ordered and bounded.
- Lifecycle states and failure causes are explicit and observable.
- Runtime observation identities are bounded before execution.
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
[hardening evidence](docs/hardening.md). Operational integrations are covered
by [runtime observability](docs/observability.md) and
[maintenance mode](docs/maintenance.md). Explicit owned policy construction,
execution scope, lifecycle, and diagnostics are covered by
[resilience composition](docs/resilience.md). The
[release evidence matrix](docs/evidence.md) maps every material promise to
implementation, tests, and public contracts.

## Package surface

| Package | Responsibility |
| --- | --- |
| `service` | Typed commands, cohesive construction, lifecycle, signals, supervision, and ordered cleanup |
| `serverhttp` | Secure HTTP defaults, serving, draining, and middleware |
| `healthhttp` | Startup, liveness, readiness, and dependency checks |
| `integration` | Dependency-neutral hooks for caller-owned facilities |
| `servicetest` | Deterministic lifecycle and probe test utilities |

## Five-minute service

```go
package main

import (
    "context"
    "os"

    "github.com/faustbrian/golib/pkg/service"
)

func main() {
    os.Exit(service.Main(service.Definition{
        Identity: service.Identity{Name: "worker"},
        Commands: service.Commands{
            Worker: service.CommandFor(service.CommandSpec[struct{}]{
                Name: "worker",
                Kind: service.CommandKindLongRunning,
                Load: func(
                    context.Context,
                    service.Invocation,
                ) (struct{}, error) {
                    return struct{}{}, nil
                },
                Build: func(
                    context.Context,
                    service.BuildContext,
                    struct{},
                ) (service.Plan, error) {
                    return service.Plan{Tasks: []service.Task{{
                        Name: "worker",
                        Run: func(ctx context.Context) error {
                            <-ctx.Done()

                            return context.Cause(ctx)
                        },
                    }}}, nil
                },
            }),
        },
    }))
}
```

Save this as `main.go`, run `go mod init example`, add the module with
`go get github.com/faustbrian/golib/pkg/service`, and run it with `go run .`. Send
SIGINT or SIGTERM to stop it. Long-running commands expose `/livez`,
`/startupz`, and `/readyz` on `127.0.0.1:8081` by default. Startup follows
registration order. Failed startup rolls back only transferred components.
Shutdown withdraws readiness, joins supervised tasks, and then stops components
in reverse order. The lower-level `New`, `Run`, and `Wait` APIs remain available
for direct lifecycle composition.

Supplying `Definition.Observer` standardizes bounded runtime events while
retaining caller ownership of metrics and tracing. Supplying
`Definition.Maintenance.Store` adds `down`, `up`, and `status`, business
admission control, and a readiness overlay without changing liveness.

## Compatibility

Consumers must currently pin an exact unreleased revision. Selecting and
publishing a semantic version is a separate maintainer decision after the
platform verification gates pass; this work does not reserve a `v1.0.0` tag.

## License

MIT. See [LICENSE](LICENSE).

## Ecosystem

Use the [Golib documentation portal](https://github.com/faustbrian/golib/blob/main/docs/index.md)
to choose companion packages, supported stacks, recipes, and operations guidance.
