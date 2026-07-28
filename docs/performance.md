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
