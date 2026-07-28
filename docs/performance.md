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
performs an unrecorded warmup, records five independent process samples,
retains checksummed raw `oha` output, and enforces the frozen service budgets.
Its atomic report checkpoints each completed sample with the execution
revision and complete gate-input digest.

Five 100 ms allocation samples across the disabled reference workloads
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
loaded validation environment was unsuitable for a latency verdict, so the
frozen relative latency budget still requires quiet-host evidence.

The process report schema covers all four reference workloads and canonical
probes on the recorded environment. Allocation evidence and shared-lifecycle
worker dispatch/supervision overhead remain in the in-process benchmarks.
Compatible `net/http` candidates also record a separately started
configured-drain distribution against the declared deadline. A
quiet-host process matrix, quiet-host worker comparison, Linux, and Kubernetes
results still require recorded artifacts before release readiness can be
claimed.
