# Performance guide

The module optimizes for bounded predictable ownership, not benchmark-only
throughput. Run `make benchmark` with fixed hardware and Go version before
comparing changes. Benchmarks report allocations and are smoke-tested in CI.

Local Apple M4 Max, Go 1.26.5 baselines from 2026-07-16 use five independent
200 ms samples:

| Benchmark | Observed time | Review ceiling | Bytes | Allocations | Allocation budget |
| --- | ---: | ---: | ---: | ---: | ---: |
| lifecycle start/shutdown | 628-637 ns | 750 ns | 608 B | 7 | 10 |
| request middleware | 318-325 ns | 500 ns | 1016 B | 11 | 14 |
| readiness with two checks | 3.07-4.23 us | 6 us | 2738-2739 B | 30 | 36 |
| integration hooks without logging | 9.52-9.76 ns | 30 ns | 0 B | 0 | 0 |

Allocation budgets are enforced with `testing.AllocsPerRun` and include
headroom for supported toolchain differences. The latency ceilings are review
budgets for the recorded reference machine, not CI wall-clock assertions;
regression review must compare the same benchmark, `-benchtime`, toolchain,
CPU, and concurrency settings. Health concurrency intentionally allocates
coordination channels; middleware request IDs intentionally allocate context
and header values.

Reproduce the recorded samples with:

```sh
go test ./service ./serverhttp ./healthhttp ./integration \
  -run '^$' -bench . -benchmem -benchtime=200ms -count=5
```

Tune only after measurement. Disabling timeouts or limits to improve a
microbenchmark changes the security contract and is not a valid optimization.
