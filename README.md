# service

[![CI](https://github.com/faustbrian/go-service/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-service/actions/workflows/ci.yml)
[![CodeQL](https://img.shields.io/badge/CodeQL-required-blue)](https://github.com/faustbrian/go-service/actions/workflows/ci.yml)
[![Coverage](https://img.shields.io/badge/coverage-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Mutation](https://img.shields.io/badge/mutation-100%25_required-blue)](CONTRIBUTING.md#verification)
[![Documentation](https://img.shields.io/badge/docs-checked_in_CI-blue)](docs/)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-service.svg)](https://pkg.go.dev/github.com/faustbrian/go-service)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-service?sort=semver)](https://github.com/faustbrian/go-service/releases)
[![Go](https://img.shields.io/badge/go-1.26.6-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`service` is a standard-library-first runtime foundation for independently
deployed Go services. It coordinates lifecycle, HTTP serving, probes, and
cross-cutting hooks without choosing an application architecture, router,
logger backend, telemetry SDK, queue, database, or configuration source.

The cohesive API starts its stable release history at `v1.0.0`.

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

    "github.com/faustbrian/go-service"
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
`go get github.com/faustbrian/go-service`, and run it with `go run .`. Send
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

Consumers should pin an exact released version. Breaking API changes require a
new major release and explicit migration guidance.

## License

MIT. See [LICENSE](LICENSE).

## Ecosystem

For ecosystem-wide package selection and ownership guidance, see the versioned
[Golib ecosystem index](https://github.com/faustbrian/go-library-tools/blob/v1.3.0/docs/ecosystem/README.md)
and its [Service edge family](https://github.com/faustbrian/go-library-tools/blob/v1.3.0/docs/ecosystem/design-language.md#package-families-and-selection).
This repository's package-specific contracts and operational guidance are
indexed above and in [`docs/`](docs/).
