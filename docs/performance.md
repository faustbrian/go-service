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
| Postal JSON-RPC | 56 allocations | 56 allocations |
| Track ingestion fan-out | 62 allocations | 62 allocations |
| Track JSON-RPC fan-out | 68 allocations | 68 allocations |
| Location lookup | 61 allocations | 61 allocations |

Logging-enabled and tracing-enabled comparisons also retained identical
low-level and cohesive allocation counts for every workload. These in-process
observations prove the zero-added-steady-allocation composition claim only;
network latency and throughput remain process evidence.

The worker benchmark runs one correlation-aware long-running fixture under
both low-level and cohesive supervision. Its current validation samples record
identical steady-state costs of 64 B and two allocations per dispatch. The
low-level and cohesive medians were 1.215 us and 1.235 us respectively; the
1.0165 ratio passed the frozen 1.03 relative ceiling. The ten-sample ranges
were 9% and 13%, so this evidence supports the budget verdict rather than a
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
the low-level baseline and cohesive candidate under the sustained load. The
relative startup and no-work shutdown budgets also failed; their
single-digit-millisecond reference measurements make these ratios
noise-sensitive. These failures remain failures; the recorded environment does
not waive or redefine the frozen budgets.

The matching ten-sample microbenchmark capture is
`.artifacts/pkg/service/performance/platform-benchmarks-balanced-committed.txt`
with SHA-256
`ea7f8cde7cb7478253b9dbcf32b826d9a71d2fe0cf889fc9185a44e5e9c55718`.
Linux performance evidence and a complete passing frozen-budget verdict remain
required before release readiness can be claimed. Kubernetes lifecycle
evidence is recorded separately in `docs/hardening.md`.
