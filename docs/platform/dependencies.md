# Platform dependency graph

This document freezes the Phase 1 module and package dependency direction.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## Module graph

```text
root service --> standard library
root service --> cli
root service --> correlation
root service --> correlation/log
root service --> serverhttp --> correlation/http --> correlation
root service --> healthhttp

servicetest ----------------> root service
owning-module adapters -----> root service
applications ---------------> root service and selected subpackages
```

The root `service` module MAY depend directly only on the standard library,
`cli`, and `correlation`. `cli` and `correlation` MUST NOT import `service`.

The root package MAY import `serverhttp` and `healthhttp` inside the same
module. Those subpackages MUST NOT import the root package.

## Internal package direction

```text
service (root)
  +-- serverhttp
  |     +-- correlation/http
  +-- healthhttp
  +-- cli
  +-- correlation

servicetest ------> service
applications -----> service
applications -----> serverhttp
applications -----> healthhttp
```

`healthhttp` currently imports the nested lifecycle package. During root
consolidation it MUST replace that dependency with a local `StateSource`
contract whose state values do not require importing root `service`.

`serverhttp` MUST replace `RequestIDs` ownership with
`correlation/http`. It MAY retain only a temporary pre-release migration bridge
that is absent from the final API baseline.

## Owning-module adapters

Adapter directories and package identifiers are frozen as follows:

| Owning module | Directory | Package | Imports root `service` |
| --- | --- | --- | --- |
| `config` | `pkg/config/configservice` | `configservice` | yes |
| `postgres` | `pkg/postgres/postgresservice` | `postgresservice` | yes |
| `cache` | `pkg/cache/cacheservice` | `cacheservice` | yes |
| `kafka` | `pkg/kafka/kafkaservice` | `kafkaservice` | yes |
| `queue` | `pkg/queue/queueservice` | `queueservice` | yes |
| `scheduler` | `pkg/scheduler/schedulerservice` | `schedulerservice` | yes |
| `telemetry` | `pkg/telemetry/telemetryservice` | `telemetryservice` | yes |
| `migrations` | `pkg/migrations/migrationsservice` | `migrationsservice` | yes |

These packages adapt concrete owning-module types to lifecycle, readiness, and
command contracts. They MUST NOT hide the concrete client API or cause
`service` to import the owning module.

`correlation` and `cli` are direct lower-level dependencies and do not receive
service adapters. The root uses `correlation/log` only to attach disclosure-
controlled typed identifiers to the caller-owned logger. Existing correlation
HTTP, JSON-RPC, queue, schedule,
webhook, logging, and telemetry adapters are reused. Kafka adds its missing
correlation adapter in the Kafka module.

## Adapter contracts

Each lifecycle adapter MUST state:

- who constructs and owns the concrete resource;
- whether ownership transfers;
- startup validation and its timeout;
- readiness inclusion and recovery;
- drain order;
- shutdown and partial-start cleanup;
- retry responsibility;
- error classification;
- repeated shutdown behavior; and
- whether the resource may be shared.

The fixed policies are:

| Adapter | Construction and ownership |
| --- | --- |
| config | returns a typed validated value before component construction; owns no long-lived resource |
| postgres | accepts an explicit constructor or pool; optional bounded ping; closes only transferred pools |
| cache | accepts explicit cache/Valkey resources; readiness is opt-in; never closes shared resources |
| Kafka producer | optional bounded startup validation; flush/close after producers drain |
| Kafka consumer | stops intake, joins deliveries, then closes; queue policy stays in Kafka |
| queue producer | drains publishers before transport close |
| queue worker | stops intake, joins in-flight work, then releases the concrete queue |
| scheduler | stops scheduling, joins active executions, then closes owned facilities |
| telemetry | initializes explicit providers and performs bounded flush/shutdown without implicit globals |
| migrations | adapts a `cli` one-shot command and owns no migration semantics |

## Composition-only modules

The following modules require compiled fixtures but no new lifecycle adapter:

- `log`;
- `http-client`;
- `http-middleware`;
- `router`;
- `authentication`;
- `authorization`;
- JSON-RPC;
- JSON:API; and
- OpenAPI-generated handlers.

Fixtures MUST prove dependency direction and public compatibility. They MUST
not make any of these modules a root dependency.

## Deployment-only integrations

Infisical, Better Stack, OpenTofu, Kubernetes, and UpCloud remain documentation
and deployment concerns. They MUST NOT appear in the core runtime dependency
graph.

## Architecture enforcement

Architecture tests MUST parse production imports and reject:

- imports from root `service` to an owning infrastructure module;
- imports from `cli` or `correlation` to `service`;
- imports from `serverhttp` or `healthhttp` to root `service`;
- cycles among owned modules;
- production `init` functions;
- permanent `replace` directives; and
- service adapters outside the owning modules listed above.

The repository manifests MUST declare the new root package and every adapter
package before the affected gate can pass.
