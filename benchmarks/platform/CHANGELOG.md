# Changelog

All notable changes to this benchmark module are documented here.

## Unreleased

### Added

- equivalent Postal-style request behavior across plain `net/http`, low-level
  and cohesive `service`, Chi, Gin, Echo, and Fiber/fasthttp fixtures
- separate disabled-logging, enabled-logging, and enabled-tracing benchmarks
- frozen response, safety-boundary, and untrusted-correlation regressions
- isolated process binaries with equivalent business, probe, correlation,
  shutdown, and signal behavior
- checksummed process measurements for startup, RSS, binary size, network
  percentiles, throughput, probes, and graceful shutdown
- equivalent Track ingestion and JSON-RPC fan-out plus Location lookup
  workloads in both allocation and isolated-process comparisons
- equivalent low-level and cohesive worker dispatch, correlation, supervision,
  drain, and steady-state allocation measurement
- configured-drain process samples that keep bounded work in flight after
  request cancellation and enforce the declared shutdown deadline

### Fixed

- resume an interrupted process run from its verified, input-identical sample
  checkpoints instead of discarding completed evidence
- apply frozen absolute performance and resource budgets only on the pinned
  Darwin reference environment while retaining portable success and drain
  requirements plus every relative budget on Linux
- apply the cohesive platform's response security header to every process
  comparison candidate so the measured business middleware remains equivalent
- encode Postal and Location response values with HTML-safe JSON escaping
- refresh standalone module metadata for current owned correlation, identifier,
  and service dependency content
- apply the frozen 95% significance rule to paired relative latency,
  throughput, startup, and shutdown comparisons instead of failing on a noisy
  summary ratio alone
- upgrade `quic-go` to v0.59.1 to prevent HTTP/3 QPACK trailer expansion from
  exhausting memory
- warm every candidate before timing and alternate candidate direction between
  process samples to reduce order bias from sustained host load
- enforce the frozen six-MiB cohesive binary ceiling in the focused process
  regression as well as the full measurement report
- refresh the local service checksum after integration so isolated benchmark
  gates resolve the current platform source
