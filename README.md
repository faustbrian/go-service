# service

[![CI](https://github.com/faustbrian/go-service/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/faustbrian/go-service/actions/workflows/ci.yml)
[![Documentation](https://img.shields.io/badge/docs-adoption_and_API-blue)](docs/index.md)
[![Go Reference](https://pkg.go.dev/badge/github.com/faustbrian/go-service/v3.svg)](https://pkg.go.dev/github.com/faustbrian/go-service/v3)
[![Release](https://img.shields.io/github/v/release/faustbrian/go-service?sort=semver)](https://github.com/faustbrian/go-service/releases)
[![Go](https://img.shields.io/badge/go-1.27.0-00ADD8?logo=go)](https://go.dev/)
[![License](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

`service` is a standard-library-first runtime foundation for independently
deployed Go services. It coordinates lifecycle, HTTP serving, probes, and
cross-cutting hooks without choosing an application architecture, router,
logger backend, telemetry SDK, queue, database, or configuration source.

The cohesive API starts its stable release history at `v1.0.0`. Use it when
your application needs explicit startup, probes, supervision, and bounded
shutdown across caller-owned resources. Use plain `net/http` or your existing
runtime when it already owns those concerns; `service` is not a framework,
protocol implementation, durable queue, or dependency-injection container.

## Install

The v3 module requires Go 1.27.0 or later. Pin it, for example
`go get github.com/faustbrian/go-service/v3@v3.0.0`. Subpackages share that
module. v3 is the supported line; see [adoption](docs/adoption.md#migrating-to-v3)
to migrate from v2. The nested integration, compatibility, and benchmark
modules are non-releasable engineering fixtures, not additional application
dependencies.

For a multi-package baseline, use the version roster in the published
[compatibility set](https://github.com/faustbrian/go-library-tools/blob/v1.8.4/docs/ecosystem/compatibility-sets.md),
not a mixture of latest releases. That frozen set uses `service` v1.1.0 and
does not certify later releases or application deployments. See
[compatibility](docs/compatibility.md) and [migration](docs/migration.md).

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

Start with the [documentation map](docs/index.md) for adoption, operations,
troubleshooting, examples, and contributor routes.

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

    "github.com/faustbrian/go-service/v3"
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

Save this as `main.go`, run `go mod init example`, install the released module
[as above](#install), and run it with `go run .`. Send
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

## Development

Use the toolchain in [`.go-version`](.go-version) and the checksum-pinned
public verifier declared in [`.golib.yaml`](.golib.yaml); public minima remain
Go 1.27.0.
`make check` and `make ci` select bounded checks, including existing lifecycle
race assertions. `make check-full` selects original full campaigns. All nine
modules remain separate; all-module durability checks retain PostgreSQL/Valkey
and integration tags. See [contributing](CONTRIBUTING.md#verification) for
standalone tests, seeds versus active fuzz smoke, optional focused mutation,
tooling installation and explicit composition/release gates.

## License

MIT. See [LICENSE](LICENSE).

## Ecosystem

For current adoption routes, see the
[Golib portal on main](https://github.com/faustbrian/go-library-tools/blob/main/docs/ecosystem/README.md).
The published [ecosystem snapshot](https://github.com/faustbrian/go-library-tools/blob/v1.8.4/docs/ecosystem/README.md)
and [Service edge design language](https://github.com/faustbrian/go-library-tools/blob/v1.8.4/docs/ecosystem/design-language.md#package-families-and-selection)
are versioned compatibility guidance. This repository's package-specific
contracts and operational guidance are indexed in the
[documentation map](docs/index.md).
