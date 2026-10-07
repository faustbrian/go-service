# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt PostgreSQL v2.0.0's service adapter while preserving borrowed-pool
  lifecycle and reference-definition composition.
- Adopt published Retry v2.1.0 and Resilience v2.0.0 for the existing shared
  budgets, preserving role lifecycle and bounded fleet work with v2 attempt lineage.
- Adopt Config v2.0.0 through its major-version module path while preserving
  the Postal dotenv and service-loader composition. Select Service v1.1.0 to
  satisfy Config's public dependency contract.
- Adopt Migrations v2 through its required major-version import path while
  preserving the Postal and Location migration command compositions.
- Adopt Rate Limit v2 through its major-version import path while preserving
  bounded process-local admission and drain behavior.
- Require Go 1.27.0 as the module language and minimum supported toolchain.
- Adopt the canonical service adapters published by Config, Kafka, Lease,
  Migrations, PostgreSQL, and Telemetry together with Correlation v1.1.0.
- Adopt Scheduler v2.0.0 through its major-version module path and canonical
  service adapter while preserving scheduled-role composition.
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
