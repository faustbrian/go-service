# Platform consumer matrix

This document freezes the Phase 1 construction inventory used to design the
cohesive `service` platform. The reference applications are evidence and
consumers; none is the platform template by itself.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## Frozen sources

The inventory was performed on 2026-07-28. Pull request base and head
identities were read from GitHub and matched to the listed local worktrees.

| Source | Base | Head | Local evidence |
| --- | --- | --- | --- |
| `golib/pkg/service` | n/a | `b50b43c56631b861b1705e01874689ed4bf0565c` | current `main` checkout |
| `shipitfi/mono` PR 6, Location | `8237ed78cc6692cf57a92a9008d20b536ec10e50` | `c4267d92fb5dc64c37015475c75ec7c36c1fd60a` | `feature/apps-location-go-rewrite` worktree |
| `shipitfi/mono` PR 7, Postal | `8237ed78cc6692cf57a92a9008d20b536ec10e50` | `67f72ad27227b962e7ae2b68c598441e20a369b6` | `feature/apps-postal-replacement` worktree |
| `shipitfi/mono` PR 8, Track | `8237ed78cc6692cf57a92a9008d20b536ec10e50` | `2239e4341701aeb29008a321b295c735264567e8` | `feature/track-go-replacement` worktree |

Later consumer validation MUST record new head revisions rather than silently
replacing these Phase 1 baselines.

## Classification

Each concern is assigned one primary platform disposition:

1. **identical**: the platform owns one behavior for every service;
2. **typed option**: the platform owns behavior with an explicit typed choice;
3. **adapter**: the owning module integrates with the platform;
4. **application-owned**: business or protocol composition remains local;
5. **accidental divergence**: current differences have no product reason; or
6. **genuine difference**: the platform preserves materially different needs.

## Construction comparison

| Concern | Track | Postal | Location | Disposition |
| --- | --- | --- | --- | --- |
| executable shape | one `track` binary with custom command selection | production binary is not yet composed; policy and evidence tools exist | six separate role binaries | 1, 2, 5 |
| standard roles | `serve`, named workers | role enum already contains `serve`, `worker`, `schedule`, `migrate` | API, worker, scheduler, migrate, online migrate, activate | 1, 2, 6 |
| argument parsing | application command selector and per-command parsing | `flag`-style tool parsing only | repeated `flag` and direct argument parsing | 1, 5 |
| help/version | custom and incomplete | tool-specific | command-specific and incomplete | 1, 5 |
| service identity | literal Track name plus build version in telemetry | typed identity configuration | repeated command labels and build metadata in selected commands | 1, 2, 5 |
| configuration | typed `config` loading in bootstrap | typed `config`, environment selection, bounded local `.env` | typed runtime loader with direct AWS Secrets Manager support | 1, 2, 3, 5 |
| secret delivery | environment/config package | environment and optional local `.env` | application runtime calls AWS Secrets Manager | 3, 4, 5 |
| logger | JSON `slog` plus owned redaction package | not yet composed | command-local text output and runtime-owned logging | 2, 3, 5 |
| telemetry | caller-owned `telemetry.Runtime`, explicitly non-global | not yet composed | runtime-specific instrumentation | 2, 3, 5 |
| business HTTP | `serverhttp` and application handler | JSON-RPC transport exists, runtime not composed | custom runtime HTTP server and router | 1, 2, 4, 5 |
| management HTTP | probes share the business listener | health handler is independently constructible | role-specific health is mounted by local runtime | 1, 5 |
| health paths | `/livez`, `/startupz`, `/readyz` in current Track helper | canonical paths | application runtime paths and role health | 1, 5 |
| readiness checks | PostgreSQL and Kafka helpers | lifecycle-only helper | PostgreSQL, Valkey, worker, and scheduler-specific checks | 1, 2, 3 |
| correlation | legacy `serverhttp.RequestIDs` semantics | not composed at ingress | application transport behavior | 1, 3, 5 |
| middleware | recovery, request ID, body limit plus application stack | JSON-RPC transport owns protocol behavior | custom router/auth/runtime stack | 1, 2, 3, 4 |
| PostgreSQL | application-local lifecycle wrapper | outbound adapters exist; runtime composition pending | several role-specific pool constructors and health helpers | 3, 5, 6 |
| Valkey | not required by current Track bootstrap | worker adapter exists | role-specific client and queue construction | 3, 6 |
| Kafka | application-local producer lifecycle wrapper | not required by current Postal work | not required by current Location work | 3, 6 |
| queue/worker | service task plus application worker command | Valkey worker implementation exists | dedicated worker runtime and service | 3, 4, 6 |
| scheduler | not selected in current Track command surface | schedule role planned | dedicated scheduler runtime and service | 3, 4, 6 |
| migrations | migration source pool is application-local | migration role planned | schema, online, and activation commands have different domain work | 3, 4, 6 |
| signals | one `signal.NotifyContext` in `main` | production path pending | repeated in every role binary | 1, 2, 5 |
| startup rollback | low-level `service` plus local resource wrappers | production path pending | implemented independently per runtime | 1, 3, 5 |
| draining | HTTP and task drain helper | production path pending | worker/scheduler/runtime-specific | 1, 2, 3, 5 |
| shutdown | local timeout constants and repeated close logic | configured budget exists | repeated command and runtime shutdown paths | 1, 2, 3, 5 |
| exit mapping | mostly zero or one | tool-specific | zero, one, or two depending on command | 1, 5 |
| container entrypoint | one command binary | target still being assembled | several binaries selected by workload | 2, 4, 6 |

## Reference-service findings

### Track

Track already proves that the low-level packages compose, but its
`internal/bootstrap` directory repeats platform-owned command selection,
logger and telemetry initialization, lifecycle adaptation, readiness,
listener construction, draining, signal handling, and exit rendering.
Carrier construction and Track settings remain application-owned.

The current `serve` helper mounts operational probes on the business listener.
It also uses `serverhttp.RequestIDs`, whose request/correlation semantics do
not match the owned `correlation` module. Both divergences MUST be removed by
the shared platform boundary.

### Postal

Postal is the least complete runtime baseline. Its active work contains a
strong typed configuration and local `.env` policy plus a canonical health
handler, but no complete production `serve`, worker, scheduler, or migration
bootstrap. The platform MUST validate Postal without assuming absent runtime
code is already reduced. New Postal bootstrap code that duplicates a platform
concern counts against its adoption budget.

### Location

Location demonstrates genuine role diversity: API, worker, scheduler, schema
migration, continuous online migration, and catalogue activation do not share
business behavior. It also contains the largest accidental construction
divergence: repeated signals, configuration loading, AWS secret clients, pool
ownership, error rendering, and shutdown logic.

The service platform MUST unify the generic process surface while preserving
the distinct online-migration, activation, provider, RPC, worker, and scheduler
contracts. Direct AWS secret retrieval MUST remain outside the core platform.

## Existing `golib` consumers

The current repository consumers that are outside this module are:

- `pkg/lease/leaseservice`, which adapts a managed lease to
  `service/integration.Hooks`; and
- `pkg/http-middleware/integration/siblings`, which verifies compatibility
  with `serverhttp` middleware.

Within this module, examples, compatibility tests, `healthhttp`,
`integration`, and `servicetest` import the nested lifecycle package. Every
one of those imports MUST migrate atomically when lifecycle moves to the root.

## Frozen conclusions

- One command model MUST replace role-specific argument, help, version, signal,
  and exit handling.
- One dedicated management server MUST replace probes mounted on business
  listeners.
- The root package MUST compose existing low-level behavior rather than clone
  it.
- Configuration values, routers, RPC methods, database queries, queue payloads,
  schedules, migrations, and provider construction MUST remain
  application-owned.
- PostgreSQL, cache/Valkey, Kafka, queue, scheduler, telemetry, and migrations
  MUST integrate through owning-module adapters.
- Track, Postal, and Location MUST retain different declared dependency sets;
  registration alone MUST NOT initialize a dependency.
- Consumer spikes MUST use these pinned baselines and the frozen budgets in
  `adoption-budgets.md`.
