# Hardening evidence and release verdict

The requirement-by-requirement mapping from source contracts to executable
proof and public documentation is maintained in `docs/evidence.md`.

## Lifecycle and ownership matrix

| Resource | Owner | Cancel or close path | Join or terminal proof |
| --- | --- | --- | --- |
| component | `service.Service` after successful start | reverse `Stop` | stop returns or caller bound fails |
| supervised task | `service.Service` | service context cause | shutdown waits for task count zero |
| OS signal subscription | `Run` or `Wait` | deferred `signal.Stop` | no helper goroutine is created |
| HTTP listener | `serverhttp.Server` after `New` | pre-run `Close`, or `Shutdown` then forced `Close` | repeated `Close` retains result; `Run` receives `Serve` result |
| HTTP request body | `net/http` and handler | server/request cancellation | handler contract |
| dependency check | `healthhttp.Probes` | per-check context | result or bounded semaphore quarantine |
| logger/provider | application | application policy | never owned by this module |

## Threat model

Covered hostile conditions include oversized known and streaming bodies,
header injection through request IDs, invalid option combinations, panic before
and after response commit, signal delivery in a subprocess, concurrent and
abandoned shutdown callers, partial startup rollback, cleanup failure and
panic, parent cancellation, cancellation-ignoring health checks, probe
saturation, and forced HTTP close failure.

Real optional-module tests additionally cover authentication-before-
authorization ordering, sensitive configuration-source failure, telemetry
registration collision and rollback, scheduler cancellation, and queue release.

Slow headers, reads, writes, and idle connections are bounded by independent
`http.Server` settings and exercised against real TCP listeners. Oversized
headers are rejected before the application handler; disconnects cancel request
contexts; recovery preserves standard-library flushing and HTTP/1 hijacking.
HTTP/2 behavior is delegated to and exercised through Go's standard-library
protocol configuration because this module does not replace `http.Server`.

## Findings

| ID | Severity | Finding | Disposition |
| --- | --- | --- | --- |
| H-001 | high | concurrent first barrier waiters could double-close | fixed with `sync.Once` and race regression |
| H-002 | high | `Wait` replaced its parent cause with shutdown | fixed with cause-preserving regression |
| H-003 | high | checks above concurrency could be skipped | fixed with bounded queue regression |
| H-004 | high | probe capture buffered before truncation | fixed with write-time bound regression |
| H-005 | high | stop hooks could trap shutdown callers | fixed with owned cleanup coordinator |
| H-006 | high | shutdown during startup could start later components | fixed with post-hook cancellation regression and rollback proof |
| H-007 | high | nil owned signals could hang `os/signal.Stop` | rejected before registration with `Run` and `Wait` regressions |
| H-008 | high | failed startup reported a successful startup probe after rollback | startup now succeeds only in ready or draining states |
| H-009 | high | supervised goroutines had no active-count bound | added default, configurable, and hard task ceilings with deterministic saturation proof |
| H-010 | high | an owned HTTP listener had no close path before `Run` | added repeatable `Server.Close` with pre-run listener and active-run regressions |
| H-011 | high | concurrent probes created one waiting goroutine per registered check before applying the shared limit | acquire the global slot before scheduling check work, with deterministic goroutine-bound and cancellation regressions |
| H-012 | high | context-aware scheduler loops turned graceful cancellation into a supervised task failure | classify task results matching the canceled context or cause as normal shutdown, while retaining unrelated failures |
| M-001 | medium | statement-free root and examples confused coverage scope | root skipped only when no statements; examples build in docs gate |
| M-002 | medium | HTTP requests lacked the run context by default | fixed with real-listener cause regression |
| M-003 | medium | configured HTTP bounds lacked wire-level adversarial proof | closed with independent slow header, body, write, idle, header-size, disconnect, hijack, and HTTP/2 regressions |
| M-004 | medium | nil-returning constructor middleware silently became 404 | rejected during construction like `Chain` |
| M-005 | medium | duplicate logging options silently replaced ownership | rejected as invalid integration configuration |
| M-006 | medium | local safety and fuzz scripts assumed undeclared `rg`; safety could silently pass when it was absent | replaced with standard shell tools and an isolated restricted-PATH regression |
| M-007 | medium | hosted setup reused Go 1.25.11 after GO-2026-5856 was fixed in 1.25.12 | every setup-go gate now resolves the latest available patch instead of trusting the runner cache |
| M-008 | medium | stand-in examples did not compile or execute the real optional module contracts | added an isolated pinned compatibility module with race, vulnerability, hosted drift, sensitive configuration failure, and telemetry collision gates |
| L-001 | low | ignored check cancellation can retain a goroutine | bounded globally, documented contract, later probes saturate safely |
| L-002 | low | `net/http` cannot retract committed panic output | panic contained, limitation documented |

## Current evidence

Local evidence on 2026-07-16:

- `go mod tidy -diff`: passed with no module changes;
- `make check FUZZ_TIME=5s BENCH_TIME=200ms`: passed;
- `make integration-compatibility`: passed against the pinned real optional
  module graph;
- exact statement coverage: 100.0% for `service`, `serverhttp`, `healthhttp`,
  `integration`, and `servicetest`;
- `go test -race ./serverhttp -count=20`: passed;
- `go test -race ./service ./healthhttp ./integration ./servicetest
  -count=20`: passed;
- `cd compatibility && go test -race ./... -count=10`: passed;
- all five fuzz targets: passed five-second runs;
- all four allocation benchmarks and regression budgets: passed;
- five-sample reference benchmarks stayed within their documented review and
  allocation ceilings;
- core and optional-graph `govulncheck`: no vulnerabilities found;
- examples: all compiled;
- real-listener HTTP hardening suite: passed under the race detector;
- GitHub workflow syntax and expressions: passed with `actionlint` v1.7.12.
- concurrent lifecycle, signal-storm, normal task-return, recovering-check,
  duplicate-middleware, hook-context, and log-attribute regressions: passed.
- the synctest health regression bounded scheduled check goroutines and joined
  every package-owned goroutine before its bubble completed;
- the real sensitive `config` failure preserved `errors.Is`, redacted its
  cause text, and prevented later startup;
- dependency-boundary and no-initializer architecture tests rejected temporary
  representative violations, then passed after fixture removal;
- the AST safety gate rejected `unsafe`, grouped cgo, and `go:linkname`
  fixtures under a restricted tool path;
- a disposable clone ran the release script, created a local OpenPGP-signed
  `v1.0.0` tag at its exact `main`, and passed `git verify-tag`.

Hosted evidence on 2026-07-16:

- CI run `29477292862` passed the complete local-equivalent gate on Go 1.25.12
  and all six minimum/current Go jobs on Linux, macOS, and Windows;
- Security run `29477292923` passed its reachable vulnerability scan;
- Optional integrations run `29477292940` passed the pinned real-module race
  and reachable vulnerability gate;
- all three successful workflows ran on published commit
  `341d9c045c674bca1dfb2c49431e49f38684cc78`.

## Release verdict

The implementation is a locally and hosted-verified `v1.0.0` release
candidate. Release publication is a separate maintainer action and is outside
this task. No remote tag or published release was requested or created, so this
verdict does not claim that `v1.0.0` is released.
