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
