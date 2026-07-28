# Frozen service adoption budgets

This document freezes the Phase 1 bootstrap-reduction baselines and targets for
Track, Postal, and Location.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## Counting method

The baseline unit is a nonblank, noncomment source line. The audit excludes
blank lines, `//` lines, and block-comment lines. Generated code, tests,
fixtures, and business/domain packages are excluded.

Files are included only when their primary responsibility is generic process
construction or when a whole command wrapper repeats generic process
construction. Mixed business settings and provider composition are excluded
from the numeric baseline and remain subject to the structural concern gate.

The spike uses the same method on the replacement. Moving code to a new local
helper, generated file, test helper, build tag, or another application package
does not remove it from the count.

## Frozen revisions

| Service | Revision |
| --- | --- |
| Track | `2239e4341701aeb29008a321b295c735264567e8` |
| Postal | `67f72ad27227b962e7ae2b68c598441e20a369b6` |
| Location | `c4267d92fb5dc64c37015475c75ec7c36c1fd60a` |

## Track baseline

| File | Lines |
| --- | ---: |
| `cmd/track/main.go` | 26 |
| `internal/bootstrap/cli.go` | 158 |
| `internal/bootstrap/commands.go` | 49 |
| `internal/bootstrap/serve.go` | 333 |
| `internal/bootstrap/worker.go` | 182 |
| `internal/bootstrap/logger.go` | 104 |
| `internal/bootstrap/telemetry.go` | 54 |
| `internal/bootstrap/database.go` | 193 |
| `internal/bootstrap/kafka.go` | 108 |
| `internal/bootstrap/migration_source_database.go` | 109 |
| **total** | **1316** |

`settings.go` and `carriers.go` are excluded because they primarily express
Track-owned configuration and carrier construction.

Track MUST retain at most 500 generic bootstrap lines: a reduction of at least
816 lines and 62.0%.

## Postal baseline

| File | Lines |
| --- | ---: |
| `internal/bootstrap/config.go` | 195 |
| `internal/bootstrap/health.go` | 18 |
| **total** | **213** |

Postal is incomplete at the pinned revision. Its target therefore includes a
growth guard: any new command, signal, health, management, lifecycle, or exit
code wiring that duplicates public platform behavior counts in the result.

Postal MUST retain at most 125 generic bootstrap lines: a reduction of at least
88 lines and 41.3%, with no duplicated platform concern added after the
baseline.

## Location baseline

| File | Lines |
| --- | ---: |
| `cmd/location/main.go` | 171 |
| `cmd/location-worker/main.go` | 133 |
| `cmd/location-scheduler/main.go` | 137 |
| `cmd/location-migrate/main.go` | 145 |
| `runtimeconfig/loader.go` | 303 |
| `runtimeconfig/aws_secrets_manager.go` | 64 |
| `runtimehttp/server.go` | 141 |
| `runtimehttp/role_health.go` | 62 |
| `runtimehttp/dependency_group.go` | 90 |
| `runtimepostgres/health.go` | 62 |
| `runtimevalkey/client.go` | 134 |
| `runtimeworker/service.go` | 112 |
| `runtimescheduler/service.go` | 152 |
| **total** | **1706** |

Paths are relative to
`apps/location/internal/location/infrastructure` unless they begin with
`cmd/`.

Online migration and activation command files are excluded because most of
their lines implement domain migration, reconciliation, reporting, and
activation. Their duplicated generic signal/configuration/exit concerns remain
subject to the structural gate.

Worker and scheduler `runtime.go` files are excluded because they primarily
compose Location providers, schedules, handlers, and concrete dependencies.
Their generic resource acquisition and rollback remain subject to the
structural gate.

Location MUST retain at most 650 generic bootstrap lines: a reduction of at
least 1056 lines and 61.9%.

## Structural concern gate

Raw line reduction is insufficient. Every spike MUST remove application-local
ownership of these generic concerns:

1. standard command selection, help, and version;
2. process signal subscription and repeated-signal policy;
3. stable exit-code mapping and safe error rendering;
4. management listener and canonical probe mounting;
5. correlation creation and HTTP propagation;
6. lifecycle startup, rollback, drain, shutdown, and joins;
7. generic configuration orchestration;
8. logger and telemetry lifecycle orchestration;
9. generic PostgreSQL, cache/Valkey, Kafka, queue, and scheduler lifecycle
   wrappers when an owning-module adapter exists; and
10. default HTTP safety middleware and timeout wiring.

A service MAY retain typed options, business handlers, configuration schemas,
provider construction, queries, payloads, schedules, migration operations,
and explicit selection of concrete adapters.

## Per-service required proof

### Track

The spike MUST demonstrate `serve` and worker roles, PostgreSQL and Kafka
adapters, business HTTP, separate management HTTP, telemetry, and Track
carrier construction without moving carrier logic into `service`.

### Postal

The spike MUST demonstrate `serve`, worker, schedule, and migrate definitions
even if some use bounded fixtures while active application composition remains
unfinished. It MUST preserve typed local `.env` behavior through
`configservice`.

### Location

The spike MUST demonstrate API, worker, scheduler, schema migration, online
migration, and activation commands. The platform MUST unify generic process
behavior without combining their business dependencies.

## Anti-gaming rules

The result fails when it:

- moves generic code to another application-local package;
- hides dependencies behind a locator, registry, reflection, or globals;
- counts comments, generated indirection, or tests as production reduction;
- omits a generic concern instead of adopting its platform behavior;
- changes business behavior to simplify bootstrap;
- initializes dependencies unused by the selected role; or
- lowers a frozen target after implementation begins.

Each final report includes before/after file lists, line counts, structural
concern disposition, dependency visibility, behavior evidence, and performance
results.
