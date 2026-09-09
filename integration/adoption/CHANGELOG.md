# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt the canonical service adapters published by Config, Kafka, Lease,
  Migrations, PostgreSQL, and Telemetry together with Correlation v1.1.0.
- Resolve gRPC v1.83.2 to avoid the xDS server denial-of-service issue in
  GHSA-2v4p-qf9q-27wj.
- Adopt `go-retry` v1.1.0 strict policy construction and execution for the
  reviewed in-process compositions, and pin `go-rate-limit` v1.1.0.
- Adopt `github.com/faustbrian/go-queue/adapters/service` v1.0.0 with
  `github.com/faustbrian/go-queue` v1.1.0 while preserving the existing queue
  lifecycle composition.
- Align the transitive `golang.org/x/text` dependency with the current owned
  module graph.
- Stop retaining obsolete Cobra command-line dependencies after the CLI module
  replaced its Cobra implementation.

### Added

- representative API, RPC, worker, scheduler, and one-shot resilience policy
  compositions with real bounded policy construction and lifecycle drain proof
- successive HPA feedback from retry work and local rejection signals, proving
  bounded outage amplification through scale-out, mixed rollout, and convergence

- bounded Track, Postal, and Location service-platform adoption fixtures
- compiled owning-module adapter compatibility and role-isolation evidence
- frozen bootstrap-reduction checks against the Phase 1 adoption budgets
- caller-owned correlation propagation through each reference definition
