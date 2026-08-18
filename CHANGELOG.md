# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Documentation

- Link the package README to the repository-wide Golib documentation portal.

### Added

- Add hardening simulations for stateful resilience lifecycles, concurrent
  policy activity, sustained dependency outages, replica scaling, mixed
  revisions, cold state, HPA feedback, and bounded fleet amplification.
- Bound runtime observation identities to 128 bytes and add structural guards
  against implicit policy stacks, copied resilience algorithms, and global
  policy registries.
- Add explicit component admission closure before cancellation, with
  concurrency-safe repeated drain, rollback ordering, retained failures, and
  integration hooks for stateful resilience policy lifecycle.
- Add resilience adoption evidence and guidance for named policy construction,
  inbound and outbound placement, shared budgets and deadlines, readiness,
  diagnostics, and Kubernetes replica and termination semantics.

- Add caller-owned runtime observation with identity-enriched logging and
  bounded construction, lifecycle, component, task, probe, maintenance, and
  business-request events.
- Add optional store-backed maintenance mode with `down`, `up`, and `status`,
  multi-replica storage adapters, readiness withdrawal, retry and refresh
  headers, redirects, custom responses, and secret-cookie bypass.
- Add a canonical no-workspace clean-consumer gate that resolves and exercises
  every documented public package through the repository source proxy without
  a `replace` directive.
- Allow typed service commands to declare bounded CLI options that are parsed
  before their configuration loader receives the immutable invocation.
- Add the cohesive `Main`, `Execute`, typed command, management probe, and
  business HTTP construction path with correlation-owned request identity.
- Expose base and per-connection context hooks through `serverhttp` options.
- Bound context-aware component startup with a configurable 30-second default.
- Freeze the cohesive service-platform consumer inventory, public contracts,
  dependency direction, compatibility plan, and numeric performance and
  adoption budgets required before production API implementation.
- Add bounded Track, Postal, and Location adoption fixtures that preserve
  explicit role dependencies and caller-owned correlation.
- Add an equivalent-behavior comparison harness for plain `net/http`,
  low-level and cohesive `service`, Chi, Gin, Echo, and Fiber/fasthttp.
- Add isolated process comparison evidence for startup, RSS, stripped binary
  size, JSON-RPC and probe latency, throughput, and graceful shutdown.
- Extend equivalent-behavior performance evidence with Track ingestion and
  JSON-RPC fan-out, Location lookup, and worker dispatch and supervision.
- Measure configured HTTP drain in a separately started process against the
  declared graceful-shutdown deadline.
- Add a checksum-pinned disposable Kubernetes gate that proves canonical probe
  and correlation behavior, readiness withdrawal, bounded termination,
  business-only Service exposure, and probe-free one-shot migrations.
- Allow a selected typed plan to supply its validated management listener
  configuration after application configuration loading.

### Compatibility

- Added a pinned module export baseline so incompatible public API changes
  fail the canonical repository gate.

### Changed

- Replace obsolete owned-module pseudo-version pins with the monorepo's local
  `v0.0.0` source-proxy coordinates; release tooling continues to emit exact
  `v1.0.0` dependency versions.
- Close component admission before root-runner, supervised-failure, and
  startup-rollback cancellation so accepted work observes a stable drain
  boundary.
- Remove unsupported exact-coverage and 2/2-mutation claims from the cataloged
  non-production adoption harness while retaining its behavioral, race, and
  frozen bootstrap-budget evidence.
- Supersede the earlier exact-`v1.0.0` publication decision for this platform
  goal: completion now ends at a verified commit tree, while version selection
  and tag publication require separate maintainer authorization.
- Align compatibility, integration, security, testing, and release guidance
  with the repository's Go 1.26.5 toolchain and sole owned CI workflow, and
  state that platform completion does not select or publish a `v1.0.0` tag.
- Reconcile the adoption and evidence summaries with the reviewed passing
  Darwin and Linux/arm64 process-performance reports.
- Point every hosted service-gate mapping at the repository's sole owned CI
  workflow.
- Rebaseline only the Darwin request, probe, startup, no-work shutdown, and
  cohesive idle-RSS performance budgets from reviewed default-runtime captures
  under the accepted sustained daily-work load.
- Default platform correlation uses bounded buffered UUIDv4 entropy to reduce
  per-request system randomness overhead without changing identifier semantics.
- Migrate every runnable service example and the Kubernetes workload guide to
  the cohesive command API, canonical management probes, and role-specific
  initialization contract.
- Pin owned sibling modules to immutable source revisions so service resolves
  from a clean external consumer without relying on `go.work`.

- Move the lifecycle API from the pre-release `service/service` import into the
  canonical root `service` package.
- Replace ambiguous `serverhttp` request-ID ownership with the typed
  `correlation/http` adapter.
- Join supervised tasks before closing their infrastructure components and
  retain startup success while graceful cleanup runs.
- Supervise business HTTP so listener draining begins with readiness withdrawal
  while dependency components remain available until in-flight work joins.
- Withdraw readiness immediately when the service lifetime context is
  canceled.
- Escalate a second process signal by canceling remaining work while retaining
  the original cleanup deadline and exit classification.
- Apply `Execute` signal events from command construction through startup,
  finite work, and cleanup while preserving the initiating typed cause.
- Keep an omitted caller-owned logger absent so the logging-disabled cohesive
  path carries no platform logging initialization or binary cost.
- Refresh the hardening verdict with exact final-commit hosted evidence and
  clarify that release publication is a separate maintainer action.
- Correct the platform release verdict to distinguish completed owning-module
  adapters and consumer spikes from the remaining performance, aggregate
  verification, and separately authorized publication gates.
- Record current input-fingerprinted local verification, including exact
  coverage and mutation results plus retained NilAway and SBOM warnings.
- Record the sustained daily-work performance environment and preserve the
  failed frozen absolute and lifecycle-relative budgets without waiting for an
  otherwise idle host or weakening the thresholds.
- Refresh the release evidence matrix against the current service fingerprint
  after analyzer maintenance and retain the current frozen-budget failures as
  release blockers.
- Record passing Linux/arm64 portable and relative performance budgets plus
  current complete framework and middleware-state comparison coverage while
  retaining the failed Darwin absolute budgets as release blockers.

### Added

- Add an isolated, pinned compatibility module and hosted gate that execute the
  real configuration, logging, telemetry, authentication, authorization,
  scheduler, and queue integration contracts without changing core dependencies.
- Prove real sensitive configuration failures preserve typed causes, redact
  rendered secrets, and prevent later component startup.
- Refresh five-sample lifecycle, middleware, readiness, and integration
  benchmark baselines after the concurrency hardening changes.

### Fixed

- Record the reviewed disposition of every retained service and process-harness
  NilAway advisory without representing the analyzer result as clean.
- Enforce statistically significant paired evidence before classifying a
  relative platform benchmark ratio as a regression, and distinguish the
  corrected historical relative verdict from unchanged absolute failures.
- Keep typed command options on the bounded CLI command-set path so cohesive
  service binaries remain within the frozen absolute and relative size budgets.
- Run compatibility dependency and vulnerability checks through the canonical
  repository verifier so mutable local `v0.0.0` source-proxy checksums cannot
  poison workspace verification.
- Restore clean external installation from `main` by pinning the service
  module's sibling requirements to reachable main pseudo-versions.
- Resolve unreleased sibling modules from their main-branch pseudo-versions so
  clean consumers can install the service module before tagged publication.
- Apply the shared health-check concurrency limit before scheduling check work
  so hostile probe traffic cannot create one waiting goroutine per check.
- Treat supervised task results matching their canceled context or cancellation
  cause as normal shutdown so context-aware runners do not create false errors.
- Preserve public nil-context rejection tests under direct Staticcheck and
  golangci-lint without weakening either analyzer.

## Pre-platform implementation record (unreleased) - 2026-07-16

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
