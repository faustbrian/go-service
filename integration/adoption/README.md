# Service adoption fixtures

This integration module validates that Track, Postal, and Location can replace
their generic bootstrap with the public `service` construction model while
keeping application dependencies and business behavior explicit.

It is a pre-publication compatibility fixture, not a fourth service, a shared
business package, or a runtime dependency for the three applications.

## Evidence boundary

The frozen baseline revisions remain:

| Service | Baseline revision | Baseline lines | Maximum retained |
| --- | --- | ---: | ---: |
| Track | `2239e4341701aeb29008a321b295c735264567e8` | 1316 | 500 |
| Postal | `67f72ad27227b962e7ae2b68c598441e20a369b6` | 213 | 125 |
| Location | `c4267d92fb5dc64c37015475c75ec7c36c1fd60a` | 1706 | 650 |

The consumer branches were revalidated at these later heads:

| Service | Validation head |
| --- | --- |
| Track | `bec57f886318cbcf5844ea7887fc976893b12ceb` |
| Postal | `a9a85c3bfa19df57e8f0dbf0a2d690fc02b6fc5a` |
| Location | `80b4dd5c513fd7ee978f809607b6127359af2fca` |

The numeric result counts nonblank, noncomment lines in `track.go`,
`postal.go`, and `location.go`. Concrete adapter construction is deliberately
visible in `adapters_test.go` as structural compatibility evidence. It is not
credited as removed bootstrap and no application-local helper contains the
platform behavior.

| Service | Retained lines | Reduction | Required reduction |
| --- | ---: | ---: | ---: |
| Track | 71 | 94.6% | 62.0% |
| Postal | 62 | 70.9% | 41.3% |
| Location | 92 | 94.6% | 61.9% |

Run `./scripts/check-adoption-budgets.sh` to reproduce the line gate.

## What the fixtures prove

- Track declares separate `serve` and worker roles, distinct business and
  management HTTP boundaries, and explicit telemetry, PostgreSQL, and Kafka
  lifecycle components and readiness checks.
- Postal declares `serve`, worker, schedule, and migrate roles, composes queue,
  scheduler, and migration adapters, and loads explicitly local `.env`
  configuration through `configservice`.
- Location declares API, worker, scheduler, schema migration, online migration,
  and activation roles without combining their dependency plans.
- Every reference definition preserves its caller-owned correlation factory,
  and realistic requests prove that factory owns the returned hop identities.
- Selecting a one-shot migration or activation command does not initialize
  unrelated long-running facilities.
- Owning-module adapters remain concrete at the composition boundary; the
  fixtures do not introduce a locator, registry, reflection, globals, or
  cross-service business package.
- API, RPC, worker, scheduler, and one-shot fixtures construct bounded named
  resilience policies explicitly, share one logical retry budget, exercise a
  caller-owned total deadline, and prove stateful admission closure releases
  blocked waiters before active-attempt drain and repeated shutdown.

Application routers, carrier construction, queries, payloads, schedules,
migrations, provider clients, retry policy, and activation semantics remain
outside this module.

## Verification

```sh
go test ./...
./scripts/check-adoption-budgets.sh
```

Published resolution remains blocked until the coordinated stable module
releases described by `pkg/service/docs/release.md`. Verification against the
repository's local `v0.0.0` source proxy is pre-publication evidence, not
public clean-consumer proof.
