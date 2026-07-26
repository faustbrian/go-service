# Goal Harden: `service`

## Mission

Perform an evidence-driven lifecycle, concurrency, HTTP, integration,
compatibility, and resource-safety audit of
`service`, then close every justified gap before `v1`.

Do not treat meaningful 100% coverage, race-detector success, or examples as
proof by themselves. Reconstruct the promised state machine and prove every
transition, failure path, cleanup obligation, and bound adversarially.

## Authoritative Inputs

- Go documentation for `context`, `net/http`, `os/signal`, `log/slog`, and
  runtime behavior
- relevant HTTP semantics and Kubernetes probe/lifecycle
  documentation
- `log`, `telemetry`, and OpenTelemetry contracts used by optional
  integration adapters
- `.ai/GOAL.md`, public APIs, examples, docs, tests, fuzzers, benchmarks,
  workflow definitions, dependencies, and changelog

Use primary sources and record every guarantee with source, implementation,
test, and documentation evidence.

## Phase 1: Baseline And Threat Model

1. Inventory every exported API, option, default, state, goroutine, timer,
   listener, middleware, health check, integration seam, and telemetry hook.
2. Draw startup, ready, draining, shutdown, failure, panic, and cancellation
   state machines with ownership and cleanup edges.
3. Run format, vet, lint, test, exact coverage, full race, fuzz, benchmark,
   docs, vulnerability, and workflow checks; record skips and flakes.
4. Threat-model hostile clients, slowloris behavior, oversized bodies, panic,
   signal storms, stuck dependencies, leaked secrets, and metric cardinality.
5. Add a failing regression before every behavioral correction.

## Lifecycle And Concurrency Audit

Prove behavior for:

- zero components, duplicate components, startup ordering, partial startup,
  rollback ordering, cleanup errors, and panic during start or stop
- concurrent `Start`, `Ready`, `Drain`, and `Shutdown` calls
- repeated signals, cancellation causes, parent deadline expiry, and caller
  abandonment
- goroutines that return early, panic, ignore cancellation, or block forever
- timeout ownership, timer cleanup, join behavior, and error aggregation
- readiness before startup, during partial startup, while draining, and after
  failed shutdown
- no goroutine, listener, connection, timer, or channel leaks

All concurrency tests must use deterministic barriers, not timing sleeps.

## HTTP And Middleware Audit

- Verify every server timeout independently, including zero/negative values.
- Test slow headers, slow bodies, slow responses, idle connections, keep-alive,
  HTTP/2, disconnects, hijacking where applicable, and shutdown races.
- Prove body limits before reads and panic recovery before headers are written.
- Audit request IDs, trusted inbound IDs, header injection, propagation, and
  cardinality.
- Prove middleware ordering, duplicate installation, nil handlers, partial
  writes, optional interfaces, and preservation of `Flusher`/`Hijacker` where
  promised.
- Ensure external errors disclose no stack, secret, config, or dependency data.

## Integration Audit

- Verify `config` load failures prevent partial service startup and preserve
  secret-safe errors, cancellation, and cleanup.
- Verify `authentication`, `authorization`, `scheduler`, and `queue`
  integrations preserve middleware order, cancellation, and dependency direction.
- Prove `service` does not implement configuration loading, authentication,
  or authorization policy.

## Health And Integration Audit

- Test nil, duplicate, slow, panicking, failing, recovering, and
  cancellation-ignoring checks under bounded concurrency.
- Verify status aggregation and response stability without leaking details.
- Audit log attributes, trace propagation, metric labels, disabled telemetry,
  and duplicate registration through `log` and `telemetry` integrations.
- Prove `service` does not initialize exporters, own logging handlers, or
  create dependency cycles with either integration package.
- Establish allocation and latency budgets for middleware and probes.

## Mandatory Hardening Evidence

- meaningful 100% production coverage with branch/error intent review
- full race suite and goroutine leak checks
- fuzz targets for HTTP options, health responses, and integrations
- real-listener integration tests and safe subprocess signal tests
- resource-bound tests for bodies, headers, checks, workers, and shutdown
- allocation-reporting benchmarks and stored baselines
- documentation examples compiled and executed in CI

## Required Deliverables

1. Lifecycle/state/ownership matrix and threat model.
2. Findings report with severity, evidence, reproduction, and disposition.
3. Focused regressions, fuzz seeds, fixes, and benchmark baselines.
4. Updated API, adoption, security, operations, compatibility, and changelog
   documentation.
5. Final release-readiness verdict with exact commands, remaining risks, and
   SemVer recommendation.

## Release Blockers

Block release for any unexplained leak, race, panic, unbounded operation,
ambiguous lifecycle transition, unsafe default, integration bypass, secret
disclosure, flaky test, undocumented exported behavior, coverage game, or red
CI/security gate.

## Completion Criteria

Hardening is complete only when every lifecycle and resource claim is mapped to
deterministic passing evidence, all high/medium findings are fixed or rejected
with proof, the complete quality gate passes without unexplained skips, and
documentation makes no claim stronger than the implementation guarantees.
