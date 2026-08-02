# Frozen platform performance budgets

This document freezes Phase 1 performance and resource budgets before cohesive
platform implementation.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## Reference environment

Baselines were measured on 2026-07-28 at
`b50b43c56631b861b1705e01874689ed4bf0565c`.

| Input | Value |
| --- | --- |
| OS/architecture | macOS, `darwin/arm64` |
| CPU | Apple M4 Max, 16 logical CPUs |
| memory | 128 GiB |
| Go | `go1.26.5` |
| benchmark parallelism | Go default, benchmark suffix `-16` |
| microbenchmark sample | five independent 200 ms samples |
| network sample | five runs, 100,000 requests, concurrency 16 |

Comparisons MUST use the same machine, toolchain, power mode, benchmark
duration, concurrency, handler behavior, and middleware state. Linux release
evidence records a separate environment and applies the same relative budgets.

## Observed low-level baseline

The baseline command was:

```sh
go test ./service ./serverhttp ./healthhttp ./integration \
  -run '^$' -bench . -benchmem -benchtime=200ms -count=5
```

| Operation | Observed range | Bytes | Allocations |
| --- | ---: | ---: | ---: |
| lifecycle start/shutdown | 516.3-554.0 ns | 608 B | 7 |
| request recovery, request ID, and body limit | 318.6-331.5 ns | 1016 B | 11 |
| readiness with two checks | 3.303-4.020 us | 2738 B | 30 |
| integration hook without logging | 9.295-10.02 ns | 0 B | 0 |

Correlation primitives were measured separately because the pre-platform
`serverhttp` still owns ambiguous request-ID middleware:

```sh
go test . -run '^$' \
  -bench 'BenchmarkFactoryNext|BenchmarkCarrierRoundTrip' \
  -benchmem -benchtime=200ms -count=5
```

| Correlation operation | Observed range | Bytes | Allocations |
| --- | ---: | ---: | ---: |
| create child request identity | 50.92-52.64 ns | 22 B | 1 |
| encode/decode all three identities | 223.7-225.7 ns | 496 B | 8 |

The full comparison fixture MUST combine these same low-level primitives and
MUST include distinct correlation and request IDs, recovery, body limits,
logging state, tracing state, and probes.

## Process baseline

A trimmed low-level HTTP example produced:

| Metric | Observation |
| --- | ---: |
| binary size | 5,700,546 bytes |
| startup/request/graceful-stop mean | 17.1 ms |
| startup/request/graceful-stop range | 15.1-19.8 ms |
| process start to successful probe, median | 21.8 ms |
| process start to successful probe, range | 13.2-66.4 ms |
| graceful stop after `SIGTERM`, median | 0.7 ms |
| graceful stop after `SIGTERM`, range | 0.6-15.2 ms |
| idle RSS range | 11,104-11,392 KiB |

Five loopback HTTP samples produced:

| Metric | Observed range | Median |
| --- | ---: | ---: |
| requests/second | 85,616-107,366 | 104,828 |
| p50 | 139-151 us | 142 us |
| p95 | 220-356 us | 233 us |
| p99 | 308-731 us | 349 us |
| success | 100% | 100% |

The network baseline is a noise-sensitive smoke baseline. Statistical
comparison uses medians and the relative rules below.

## Frozen absolute budgets

| Surface | Budget |
| --- | ---: |
| lifecycle start/shutdown | at most 750 ns/op, 10 allocations, 768 B |
| full correlation HTTP middleware | at most 1 us/op, 20 allocations, 1800 B |
| readiness with two immediate checks | at most 6 us/op, 36 allocations, 3200 B |
| disabled integration hook | at most 30 ns/op, zero allocations |
| process startup to successful startup probe | p95 at most 75 ms |
| idle RSS | at most 13 MiB |
| stripped reference binary | at most 6 MiB |
| loopback HTTP | p95 at most 400 us; p99 at most 800 us |
| loopback HTTP throughput | at least 85,000 requests/second |
| loopback JSON-RPC | p95 at most 500 us; p99 at most 1 ms |
| loopback JSON-RPC throughput | at least 70,000 requests/second |
| probe request | p95 at most 350 us |
| no-work graceful shutdown | p95 at most 20 ms |
| configured drain | no more than 100 ms beyond its declared deadline |

## High-level composition budget

For every behavior-matched fixture:

- the cohesive construction layer MUST add zero steady-state allocations after
  construction;
- its median p50 and p95 latency MUST be no more than 1.03 times the low-level
  composition;
- its median throughput MUST be at least 0.97 times the low-level composition;
- its startup p95 MUST be no more than 1.05 times low-level startup;
- its idle RSS MUST be no more than low-level RSS plus 512 KiB;
- its binary MUST be no more than low-level binary plus 256 KiB; and
- its no-work shutdown p95 MUST be no more than 1.05 times low-level shutdown.

Enabled logging, tracing, compression, decompression, authentication,
authorization, and rate limiting are measured as middleware costs, not
high-level bootstrap overhead. Disabled logging and tracing MUST add no
platform allocation beyond the shared mandatory pipeline.

## Workload fixtures

The acceptance suite MUST include:

- Postal-style JSON-RPC search with bounded JSON decode and multiple results;
- Track-style ingestion and JSON-RPC request shapes with correlation fan-out;
- Location-style lookup with bounded result projection;
- worker dispatch and supervision;
- liveness, startup, and readiness;
- graceful HTTP and worker drain; and
- logging and tracing independently disabled and enabled.

Business algorithms and external I/O are deterministic fixtures. Each
high-level result is compared to the exact same low-level handler and data.

## Framework comparison

Plain `net/http`, low-level `service`, cohesive `service`, Chi, Gin, and Echo
MUST implement equivalent behavior. Fiber/fasthttp is reported separately
because its runtime contract is incompatible.

Each report includes p50, p95, p99, throughput, allocations, bytes, startup,
idle RSS, binary size, shutdown, tool versions, CPU, OS, sample count, and raw
artifacts. A framework is not weakened by omitting identifiers, recovery,
limits, probes, logging, tracing, or JSON work.

## Statistical decision

Microbenchmarks use at least five samples and `benchstat` at a 95% confidence
level. A relative regression fails when the median crosses its budget and the
comparison is statistically significant. Absolute allocation, byte, RSS,
binary, error-rate, and deadline budgets fail immediately.

Network tests use at least five independent samples. Median p50, p95, p99, and
throughput are compared; any nonzero unexpected error rate fails.

These budgets are immutable during implementation. A change requires a
separate reviewed decision containing new evidence; it MUST NOT rewrite this
record merely to make an implementation pass.

## Reviewed sustained-load rebaseline

Decision D-015 in `decisions.md` supersedes only the startup, no-work shutdown,
five loopback request and probe rows, and cohesive idle-RSS allowance. The
absolute RSS, binary-size, success, configured-drain, and other high-level
composition budgets remain unchanged.

| Surface | Reviewed budget |
| --- | ---: |
| loopback HTTP | median p95 at most 6 ms; median p99 at most 17.5 ms |
| loopback HTTP throughput | median at least 9,000 requests/second |
| loopback JSON-RPC | median p95 at most 7.25 ms; median p99 at most 19.5 ms |
| loopback JSON-RPC throughput | median at least 7,000 requests/second |
| probe request | median p95 at most 6.5 ms |
| process startup to successful startup probe | p95 at most 200 ms |
| no-work graceful shutdown | p95 at most 30 ms |
| cohesive idle RSS | at most low-level idle RSS plus 1 MiB |

These values apply only when the complete reference-environment identity in
D-015 matches. They MUST NOT be used to waive the relative low-level-to-
cohesive budgets or a nonzero request error rate.
