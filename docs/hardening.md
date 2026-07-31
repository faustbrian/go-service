# Hardening evidence and release verdict

The requirement-by-requirement mapping from source contracts to executable
proof and public documentation is maintained in `docs/evidence.md`.

## Lifecycle and ownership matrix

| Resource | Owner | Cancel or close path | Join or terminal proof |
| --- | --- | --- | --- |
| component | `service.Service` after successful start | reverse `Stop` | stop returns or caller bound fails |
| supervised task | `service.Service` | service context cause | shutdown waits for task count zero |
| OS signal subscription | `Run`, `Wait`, or cohesive `Main` | deferred `signal.Stop` | command and shutdown coordinators are joined before return |
| HTTP listener | `serverhttp.Server` after `New` | pre-run `Close`, or `Shutdown` then forced `Close` | repeated `Close` retains result; `Run` receives `Serve` result |
| HTTP request body | `net/http` and handler | server/request cancellation | handler contract |
| dependency check | `healthhttp.Probes` | per-check context | result or bounded semaphore quarantine |
| logger/provider | application | application policy | never owned by this module |

## Threat model

Covered hostile conditions include oversized known and streaming bodies,
header injection through correlation metadata, invalid option combinations, panic before
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
| H-013 | high | cohesive commands ignored signals during configuration and component startup | one owned command coordinator now cancels construction, startup, runtime work, and cleanup with focused signal regressions |
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

## Current Kubernetes lifecycle evidence

On 2026-07-28, `make kubernetes` passed against checksum-pinned kind v0.31.0
and Kubernetes v1.35.0 on Docker 29.6.2. The disposable-cluster report records
the complete service and benchmark input digests and proves a ready deployment
without restarts, business-only Service exposure, canonical probe and
correlation wire contracts, readiness withdrawal before listener closure, pod
deletion in 2,211 milliseconds against a five-second grace, worker and
scheduler management lifecycles terminating in 2,737 and 2,482 milliseconds,
and a successful probe-free migration Job. The local report is
`.artifacts/pkg/service/kubernetes/report.json`; it is not hosted or
managed-cluster evidence.

## Current platform integration evidence

On 2026-07-28, focused current-tree verification passed for every mandatory
owning-module adapter: configuration, PostgreSQL, cache, Kafka, queue,
scheduler, telemetry, and migrations. The isolated compatibility module passed
against its current workspace graph, and the composition-only sibling fixture
passed for logging, HTTP client and middleware, router, authentication,
authorization, JSON-RPC, JSON:API, and generated OpenAPI handlers.

The Track, Postal, and Location adoption fixture also passed its focused tests
and frozen bootstrap budgets. It retained 71, 62, and 92 generic construction
lines respectively against maxima of 500, 125, and 650. These are
pre-publication workspace results; they do not prove published dependency
resolution or replace the final affected-module gates.

## Current performance evidence

On 2026-07-29, the complete equivalent-process matrix ran on the Apple M4 Max
reference host under the sustained daily-work load that represents the maximum
CPU availability for this work. The accepted evidence plan does not wait for a
quiet host. The matrix recorded 105 samples and 525 checksummed raw files at
source revision `625c3ca219bb341c5bb9393b6075e32648920d78`. Its report is
`.artifacts/pkg/service/performance/platform-process-balanced-committed/report.json`,
with SHA-256
`6708e49934b1c811e3a55ed5f533b055b367c528ece14b9cd179e305665e9c32`
and gate-input digest
`87d9fe1ef77cf15022b9b5d79e6d0eccd6c3877ec4ae2fce327713ce8ff9236d`.

Every low-level-to-cohesive request-relative p50, p95, and throughput budget
passed for Postal JSON-RPC, Track ingestion, Track JSON-RPC, and Location
lookup. Absolute and relative binary and RSS budgets passed. Absolute startup,
shutdown, configured-drain, and success-rate budgets also passed. Both
implementations failed frozen absolute request latency, throughput, and probe
budgets under the available sustained load. The stored report was produced by
the earlier median-only relative evaluator and therefore lists startup and
no-work shutdown failures that do not satisfy the frozen significance rule.
The preserved pairs cross those thresholds in only two of five samples for
each metric; the exact one-sided sign-test tail is 0.8125, so both relative
comparisons pass at 95% confidence. The report remains unchanged as the
historical execution artifact. The frozen thresholds are not waived or
rewritten by the environment.

The matching ten-sample, 250 ms microbenchmark capture is
`.artifacts/pkg/service/performance/platform-benchmarks-balanced-committed.txt`,
with SHA-256
`ea7f8cde7cb7478253b9dbcf32b826d9a71d2fe0cf889fc9185a44e5e9c55718`.
All low-level and cohesive allocation counts and bytes were identical for every
workload and middleware state. Worker dispatch recorded 64 B and two
allocations in both paths; its 1.0165 median latency ratio passed the 1.03
relative ceiling. The wider latency ranges are retained as variability rather
than presented as stable rankings.

## Current local gate evidence

On 2026-07-31,
`./scripts/run-modules.sh check --jobs 1 --modules pkg/service` passed and its
input-fingerprinted records were revalidated at
`1fd9820e9ca70a5f568609d3c6118ad21b3c3d1a`. The scoped module records cover
formatting, tidy, safety, vet, tests, race, exact coverage, lint, Staticcheck,
vulnerability scanning, secrets, licenses, SBOM, fuzzing, mutation,
documentation, API, interoperability policy, and package benchmarks. Coverage
was exact for every production package: 827/827 root statements, 116/116
`healthhttp` statements, 49/49 `integration` statements, and 181/181
`serverhttp` statements. Mutation killed all 547 viable mutants: 406 root, 49
`healthhttp`, 20 `integration`, and 72 `serverhttp`, with exact 100% efficacy
and mutant coverage.

The scoped run does not claim the root inventory, root command tests, workflow
lint, the complete repository matrix, or hosted CI. Earlier
`make integration-compatibility` evidence remains attributable to its recorded
fingerprint and is not rewritten as a current service-module execution.

The local evidence retains two warnings rather than describing them as clean:

- advisory NilAway exited with status 3 and reported four potential nil flows,
  covering variadic middleware slicing, one lifecycle test assertion, the
  explicit nil-context runner test, and one benchmark helper; and
- SBOM generation passed but warned that Git could not determine the main
  module version in its isolated source tree.

These warnings are not failed repository gates, but they remain visible for
the final release review.

## Historical pre-platform evidence

The following evidence was current for the pre-platform tree on 2026-07-16.
Root-package consolidation, cohesive construction, and correlation migration
changed its inputs, so none of it is current release evidence for the platform
work:

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

## Current release verdict

The module is pre-v1 and is not release-ready. Both the low-level and cohesive
runtimes still fail the frozen absolute process request and probe budgets on
the accepted available environment. The current focused report records 18
failed absolute metrics and does not replace the required complete framework
and middleware-state matrix. Current-fingerprint Linux evidence, root
repository and affected-module aggregate gates, and hosted results remain
required. The complete scoped service contract, including exact coverage and
mutation, has current input-fingerprinted evidence. Every mandatory
owning-module adapter and all three consumer validation spikes are implemented
and have focused current-tree proof.

Stable published `cli` and `correlation` dependencies, pinning `service` to
those versions, and clean-consumer verification remain publication-order
blockers. Publishing those dependencies or `service`, and creating or pushing
any tag, requires separate authorization.
