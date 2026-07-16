# Performance guide

The module optimizes for bounded predictable ownership, not benchmark-only
throughput. Run `make benchmark` with fixed hardware and Go version before
comparing changes. Benchmarks report allocations and are smoke-tested in CI.

Local Apple M4 Max, Go 1.26.5 baselines from 2026-07-16 using a short 20 ms run:

| Benchmark | Time | Bytes | Allocations |
| --- | ---: | ---: | ---: |
| lifecycle start/shutdown | 193 ns | 544 B | 6 |
| request middleware | 307 ns | 1016 B | 11 |
| readiness with two checks | 3421 ns | 2746 B | 30 |
| integration hooks without logging | 8 ns | 0 B | 0 |

These are observational baselines, not cross-machine budgets. Regression review
should compare the same benchmark, `-benchtime`, toolchain, CPU, and concurrency
settings. Health concurrency intentionally allocates coordination channels;
middleware request IDs intentionally allocate context and header values.

Tune only after measurement. Disabling timeouts or limits to improve a
microbenchmark changes the security contract and is not a valid optimization.
