# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

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
