# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Establish the module, architecture, lifecycle, security, contribution, and
  release contracts for the initial implementation.
- Add ordered lifecycle startup, rollback, draining, concurrent repeatable
  shutdown, typed failures, cancellation causes, panic containment, and
  supervised-task joins in the `service` package.
- Enforce exact coverage for packages with executable production statements
  while allowing documentation-only packages to remain minimal.
- Add bounded process runners with owned OS signal subscriptions,
  caller-managed signal channels, and signal-preserving cancellation causes.
- Add the `serverhttp` runtime with explicit listener ownership, secure timeout
  defaults, bounded draining, request IDs, body limits, panic recovery, and
  deterministic standard-library middleware composition.
- Add lifecycle-aware health handlers with stable secret-safe JSON, bounded
  concurrent or sequential dependency checks, panic containment, and explicit
  protection from cancellation-ignoring checks.
- Add dependency-neutral lifecycle hooks and optional caller-owned `slog`
  status reporting for configuration, telemetry, queue, and scheduler wiring.
- Add `servicetest` barriers, controlled components, concurrent event recording,
  and bounded HTTP probe capture for deterministic tests without sleeps.
- Add runnable HTTP API, RPC, worker, ingester, scheduled-command, and mixed-role
  adoption examples.
- Add signal-aware wait helpers for runtimes that register supervised tasks
  after startup while preserving parent cancellation causes.
- Add pinned CI, compatibility, security, fuzz, benchmark, dependency-update,
  signed-tag verification, provenance, and reproducible release automation.
- Add API, Kubernetes, migration, operations, compatibility, security,
  performance, FAQ, troubleshooting, and hardening evidence documentation.
- Queue concurrent health checks within their deadlines, propagate HTTP run
  cancellation into request contexts, bound probe capture before buffering,
  and keep timed-out component cleanup joinable.
