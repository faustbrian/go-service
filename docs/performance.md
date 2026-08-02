# Performance guide

The module optimizes for bounded predictable ownership, not benchmark-only
throughput. Run `make benchmark` with fixed hardware and Go version before
comparing changes. Benchmarks report allocations and are smoke-tested in CI.

Local Apple M4 Max, Go 1.26.5 baselines from 2026-07-28 use five independent
200 ms samples:

| Benchmark | Median (range) | Review ceiling | Bytes | Allocations | Allocation budget |
| --- | ---: | ---: | ---: | ---: | ---: |
| lifecycle start/shutdown | 620.9 ns (524.0-915.0 ns) | 750 ns | 624 B | 7 | 10 |
| full correlation HTTP middleware | 877.1 ns (807.4 ns-1.337 us) | 1 us | 1176 B | 19 | 20 |
| readiness with two checks | 3.827 us (3.303-5.475 us) | 6 us | 2802-2803 B | 32 | 36 |
| integration hooks without logging | 9.390 ns (9.336-15.58 ns) | 30 ns | 0 B | 0 | 0 |

Allocation budgets are enforced with `testing.AllocsPerRun` and include
headroom for supported toolchain differences. The latency ceilings are review
budgets for the recorded reference machine, not CI wall-clock assertions;
regression review must compare the same benchmark, `-benchtime`, toolchain,
CPU, and concurrency settings. Health concurrency intentionally allocates
coordination channels; correlation middleware intentionally allocates typed
context and response header values.

Reproduce the recorded samples with:

```sh
go test . ./serverhttp ./healthhttp ./integration \
  -run '^$' -bench . -benchmem -benchtime=200ms -count=5
```

Tune only after measurement. Disabling timeouts or limits to improve a
microbenchmark changes the security contract and is not a valid optimization.

## Equivalent framework comparison

The non-releasable `benchmarks/platform` module executes frozen Postal
JSON-RPC, Track ingestion and JSON-RPC fan-out, and Location lookup contracts
across plain `net/http`, low-level `service`, cohesive `service`, Chi, Gin,
Echo, and Fiber/fasthttp. The executable equivalence suite proves JSON
behavior, body limits, panic containment, untrusted-correlation replacement,
and optional logging and tracing before timing. Fiber remains separately
disclosed because it does not implement the `net/http` runtime contract.

The non-releasable module also provides an isolated-binary process harness. It
builds and warms every candidate before timing, groups comparisons by
middleware state, alternates candidate order between samples, records five
independent process samples, retains checksummed raw `oha` output, and enforces
the frozen service budgets. Its atomic report checkpoints each completed
sample with the execution revision and complete gate-input digest.

Ten 250 ms samples on the reference host across the disabled workloads
recorded identical low-level and cohesive allocation counts:

| Workload | Low-level | Cohesive |
| --- | ---: | ---: |
| Postal JSON-RPC | 59 allocations | 59 allocations |
| Track ingestion fan-out | 63 allocations | 63 allocations |
| Track JSON-RPC fan-out | 69 allocations | 69 allocations |
| Location lookup | 62 allocations | 62 allocations |

Logging-enabled and tracing-enabled comparisons also retained identical
low-level and cohesive allocation counts for every workload. These in-process
observations prove the zero-added-steady-allocation composition claim only;
network latency and throughput remain process evidence. The current capture is
`.artifacts/pkg/service/performance/platform-benchmarks-current.txt`, with
SHA-256
`b9d1bac23fe11d323c74786bb2b1d716b3d6222bae49b009784b4b60fe39db10`;
its `benchstat` report has SHA-256
`2ac7edfaacfcb56bc0fc0dbb5f158725ed45fbef78828a7b54f42f2f187c0da9`.

The worker benchmark runs one correlation-aware long-running fixture under
both low-level and cohesive supervision. Its current validation samples record
identical steady-state costs of 64 B and two allocations per dispatch. The
low-level and cohesive medians were 1.024 us and 0.973 us respectively; the
0.950 ratio passed the frozen 1.03 relative ceiling. The retained sample
variability means this evidence supports the budget verdict rather than a
broader claim of stable latency ranking.

The accepted available environment is the same Apple M4 Max host under its
sustained daily-work load; waiting for an otherwise idle host is not part of
the evidence plan. Relative process budgets require both a median threshold
breach and an exact one-sided paired sign test at 95% confidence. Absolute
latency, throughput, success, resource, and deadline budgets fail directly.

The 2026-07-29 process matrix at source revision
`625c3ca219bb341c5bb9393b6075e32648920d78` recorded 105 samples and 525 raw
files. Its report is
`.artifacts/pkg/service/performance/platform-process-balanced-committed/report.json`
with SHA-256
`6708e49934b1c811e3a55ed5f533b055b367c528ece14b9cd179e305665e9c32`
and gate-input digest
`87d9fe1ef77cf15022b9b5d79e6d0eccd6c3877ec4ae2fce327713ce8ff9236d`.

All low-level-to-cohesive request-relative p50, p95, and throughput budgets
passed for Postal JSON-RPC, Track ingestion, Track JSON-RPC, and Location
lookup. Absolute and relative binary-size and RSS budgets passed, as did the
absolute startup, shutdown, configured-drain, and success-rate budgets. The
frozen absolute request latency, throughput, and probe budgets failed for both
the low-level baseline and cohesive candidate under the sustained load.

The stored report predates enforcement of the frozen significance rule and
lists relative startup and no-work shutdown failures from median ratios alone.
The preserved samples cross each threshold in only two of five pairs. The exact
one-sided sign-test tail is 0.8125, so neither relative comparison is a
significant regression at 95% confidence. The report remains unchanged as the
historical execution artifact. The absolute request and probe failures remain
failures; the recorded environment does not waive or redefine those budgets.

The matching ten-sample microbenchmark capture is
`.artifacts/pkg/service/performance/platform-benchmarks-balanced-committed.txt`
with SHA-256
`ea7f8cde7cb7478253b9dbcf32b826d9a71d2fe0cf889fc9185a44e5e9c55718`.

A focused current-fingerprint run on 2026-07-31 measured the disabled
low-level and cohesive candidates with nine independently started samples
each. The report is
`.artifacts/pkg/service/performance/platform-process-postcheck-current/report.json`,
with SHA-256
`b395710079d26081dd8d2594f1b8396c9083cf441293000977f0f527181693f2`
and gate-input digest
`be19d9e8933c94686a86b44fcac096c06fa72d0d545a7a755955dd3320a9dd5f`.
All requests succeeded; startup, RSS, binary size, graceful shutdown, and
configured drain passed. Eight low-level and ten cohesive request or probe
latency and throughput metrics failed their frozen absolute budgets. This
report remains historical evidence for the superseded Phase 1 limits.

The reviewed sustained-load decision in `docs/platform/decisions.md` derives
replacement request, probe, startup, shutdown, and cohesive idle-RSS limits
from four default-runtime captures without changing success, absolute RSS,
binary-size, configured-drain, or request-relative budgets. The final focused
report is written to
`.artifacts/pkg/service/performance/platform-process-rebaseline-final-evidence/report.json`.
The report records its own SHA-256-verifiable inputs, gate-input digest, and
nine independently started samples for each disabled service candidate.
Every request and probe succeeded. Every reviewed absolute and relative
request, probe, startup, RSS, binary-size, graceful-shutdown, and configured-
drain budget passed.

A Linux/arm64 deployment-matching run at source revision
`d3095a105b545a76440ac6193862b28ad068ea51` used Go 1.26.5, checksum-pinned
`oha` 1.15.0, nine independently started samples per disabled service
candidate, 100,000 requests per business workload, 20,000 probe requests, and
concurrency 16. Its report is
`.artifacts/pkg/service/performance/platform-process-linux-arm64-relative-current/report.json`,
with SHA-256
`cb3b56f44e0b1e70b8aa5354ee0029253436c6e07b0373c6df37a41303bfc92b`
and Linux gate-input digest
`242fe5da14c73949a1429a3798d8ae091773656dd4af70f69a2fac23990200d0`.
Every request and probe succeeded. Configured drain passed, and every frozen
low-level-to-cohesive latency, throughput, startup, RSS, binary-size, and
shutdown budget passed. The cohesive maximum idle RSS was 282,624 bytes above
low-level, within the frozen 512 KiB allowance.

Current comparison coverage is split across persisted checkpoints rather than
discarding completed work after a late setup interruption. The matrix report
`.artifacts/pkg/service/performance/platform-process-linux-arm64-matrix-current/report.json`
has SHA-256
`ce2389351a620f2a09bb9449f06095a79ac52e6a89de10995d8a09debe08c2ee`
and retains five samples for all seven disabled and logging candidates. It
stopped after 95 of 105 samples when one Chi tracing configured-drain process
did not make its startup probe successful within three seconds. The separate
current-fingerprint tracing report
`.artifacts/pkg/service/performance/platform-process-linux-arm64-tracing-current/report.json`
has SHA-256
`46cc433bf565d76e0975ba3d460ead2f388fe3b647952add66efa2ebcb23c304`
and records five complete samples for all seven tracing candidates. The Chi
failure did not recur across those five samples, and every compatible tracing
candidate passed configured drain. Together, the two reports cover all seven
candidates and three middleware states with at least five completed samples;
the focused report above owns the passing Linux budget verdict.

The Darwin performance blocker is resolved by the reviewed passing report.
Kubernetes lifecycle evidence is recorded separately in `docs/hardening.md`.
