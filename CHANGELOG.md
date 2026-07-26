# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

### Changed

- Refresh the hardening verdict with exact final-commit hosted evidence and
  clarify that release publication is a separate maintainer action.

### Added

- Add an isolated, pinned compatibility module and hosted gate that execute the
  real configuration, logging, telemetry, authentication, authorization,
  scheduler, and queue integration contracts without changing core dependencies.
- Prove real sensitive configuration failures preserve typed causes, redact
  rendered secrets, and prevent later component startup.
- Refresh five-sample lifecycle, middleware, readiness, and integration
  benchmark baselines after the concurrency hardening changes.

### Fixed

- Apply the shared health-check concurrency limit before scheduling check work
  so hostile probe traffic cannot create one waiting goroutine per check.
- Treat supervised task results matching their canceled context or cancellation
  cause as normal shutdown so context-aware runners do not create false errors.
- Preserve public nil-context rejection tests under direct Staticcheck and
  golangci-lint without weakening either analyzer.

## [1.0.0] - 2026-07-16

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
- Make signal wait helpers observe supervised task failures so worker and
  mixed-role runtimes cannot remain blocked after their service is canceled.
- Add real-listener regressions for independent HTTP timeouts, hostile headers,
  disconnects, keep-alive expiry, response hijacking, and HTTP/2 operation.
- Document every exported field and enforce exported API documentation in the
  repository docs gate.
- Prevent startup from acquiring later components after cancellation, reject
  nil owned signals and nil-returning middleware, and make duplicate logging
  integration explicit configuration errors.
- Keep startup probes unavailable after failed startup rollback instead of
  treating every stopped lifecycle as successfully started.
- Bound concurrently supervised tasks with a safe default, explicit limit, hard
  ceiling, saturation error, deterministic regression, and fuzz coverage.
- Add pinned GitHub workflow validation to the local and hosted-equivalent
  `make check` gate.
- Add repeatable `Server.Close` so callers can release an owned listener when
  composition is abandoned before `Run`, with active and pre-run leak proof.
- Execute configuration, caller-owned logging, and authentication-before-
  authorization composition examples with complete error handling.
- Add enforced allocation budgets and refreshed five-sample latency baselines
  for lifecycle, middleware, readiness, and integration paths.
- Add deterministic evidence for concurrent lifecycle calls, signal storms,
  successful task return, dependency recovery, duplicate middleware, hook
  context propagation, and bounded log attributes.
- Add a release evidence matrix mapping lifecycle, HTTP, health, integration,
  resource, scenario, safety, CI, and release promises to executable proof.
- Refresh remote `main` before release comparison, prove stale-checkout
  rejection in an isolated Git remote, and make the README quickstart runnable.
- Remove the undeclared ripgrep dependency from safety and fuzz gates, and
  prove both scripts remain effective with only Go and standard shell tools.
- Require release tags to use an explicit locally available OpenPGP key that
  matches the hosted verification format instead of inheriting SSH signing.
- Add an executable queue and scheduler composition proving supervised work
  and reverse lifecycle cleanup without importing either optional module.
- Replace the RPC skeleton with a real `net/rpc` listener and expand the mixed
  service example to supervise consumer, processor, and scheduler roles.
- Prove successful health checks, startup rollback, and signal-driven shutdown
  cancel their timeout contexts immediately instead of retaining timers.
- Enforce exact production dependency boundaries and the absence of import-
  time initializers with architecture tests proven against temporary violations.
- Replace the safety regex with an AST scan so cgo imports in grouped import
  declarations cannot bypass `GO-SAFETY-1`.
- Require hosted jobs to resolve the latest Go patch so a stale runner cache
  cannot reintroduce a fixed standard-library vulnerability.
- Rehearse the complete signed-tag release flow in a disposable matching-main
  clone and record the successful OpenPGP verification evidence.
- Record the green hosted complete gate, security scan, and six-platform
  compatibility matrix on the final pre-release implementation.
