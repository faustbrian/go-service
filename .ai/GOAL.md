# Goal: `service`

## Objective

Build a serious open source Go module for constructing and operating
production services with consistent lifecycle, HTTP serving, health, and
cross-cutting integration.

The package must remain a composable runtime foundation, not an application
framework. It must work for APIs, RPC servers, ingesters, processors, workers,
and scheduled commands without controlling domain architecture.

## Product Position

`service` should be:

- open source, standard-library-first, and framework-agnostic
- suitable for independently deployed services and conventional applications
- modular, with every subpackage independently importable
- explicit about ownership, shutdown, readiness, and failure behavior
- compatible with plain `net/http`, `log/slog`, and `context`
- safe for Kubernetes and other orchestrated environments

It must not require a dependency-injection container, global registry,
specific router, database, queue, logger backend, or configuration format.

## First-Version Scope

### Core Service Lifecycle

- startup and shutdown orchestration
- `context` propagation and cancellation causes
- OS signal handling
- bounded goroutine supervision
- deterministic cleanup ordering
- startup, readiness, draining, and stopped states
- typed startup and shutdown errors

### HTTP Service Runtime

- secure `http.Server` defaults
- independently configurable read, header, write, idle, and shutdown timeouts
- connection draining and graceful shutdown
- request IDs and correlation propagation
- panic containment with safe error responses
- configurable body limits
- middleware composition without replacing `http.Handler`

### Health And Readiness

- liveness, readiness, and startup handlers
- concurrent or sequential dependency checks with explicit bounds
- stable machine-readable response contracts
- readiness transitions during startup and shutdown
- optional detailed diagnostics without leaking secrets

### Integration

- startup configuration integration through `config`
- integration hooks for standard `*slog.Logger` values and `log`
- integration hooks for OpenTelemetry providers and `telemetry`
- composition examples and optional adapters for `authentication`,
  `authorization`, `scheduler`, and `queue`
- no logging handlers, telemetry SDK lifecycle, exporter, or collector ownership

## Package Shape

Expected subpackages may include:

- `service` for lifecycle coordination
- `serverhttp` for HTTP server construction
- `healthhttp` for probes and dependency checks
- `integration` for optional `log` and `telemetry` runtime wiring
- `servicetest` for deterministic lifecycle and probe tests

The root package should remain empty or minimal. Importing one concern must not
initialize or pull in unrelated concerns.

## Non-Goals

- no web framework, router, RPC protocol, service discovery, or API gateway
- no database, cache, queue, outbox, webhook, vendor-client, logging, or
  telemetry implementation
- no authentication, authorization, idempotency, or scheduling implementation
- no configuration file, environment, discovery, merge, or validation engine;
  those belong to `config`
- no service locator or automatic dependency injection
- no business authorization or domain policy
- no hidden global state or implicit background goroutines
- no code generator or opinionated project directory layout in `v1`

## Required Design Properties

- zero values must be safe where practical and invalid configurations explicit
- every goroutine must have an owner, cancellation path, and join path
- startup failure must clean up every successfully started component
- shutdown must be bounded, repeatable, and safe under concurrent calls
- middleware ordering must be visible and deterministic
- optional integrations must not inflate the core dependency graph
- exported interfaces must be small and justified by multiple implementations
- errors must support `errors.Is`/`errors.As` where callers need policy

## Documentation Deliverables

- README and five-minute quickstart
- complete exported API documentation
- architecture, lifecycle, and ownership model
- adoption guides for HTTP API, RPC, worker, ingester, and mixed-role services
- Kubernetes startup/readiness/liveness/shutdown example
- middleware composition cookbook including `authentication` and
  `authorization`
- observability integration guide
- `config` startup and secret-delivery integration guide
- migration guide from ad hoc `net/http` service wiring
- FAQ, troubleshooting, compatibility, security, and performance guides
- runnable examples for every user-facing scenario

Documentation must let a new user adopt one subpackage or the complete runtime
without reading implementation source.

## Testing And Quality Standard

Meaningful 100% coverage of production package code is mandatory. Tests must
prove lifecycle transitions, cleanup, cancellation, timeout, panic, signal,
middleware, integration, health, and error behavior rather
than merely execute lines.

Required verification includes:

- table-driven unit and state-machine tests
- deterministic concurrency tests without timing sleeps
- full race-enabled test suite
- fuzzing for HTTP options, health payloads, and integration options
- leak tests for goroutines, listeners, timers, and response bodies
- integration tests using real HTTP listeners and process signals where safe
- allocation-reporting benchmarks for startup and request middleware paths
- hostile-input and resource-bound tests

## Repository And Release Requirements

- GitHub Actions for formatting, vet, lint, tests, exact meaningful coverage,
  race, fuzz-target smoke tests, benchmarks, docs, `govulncheck`, and releases
- `make safety` and `make check` matching CI
- `GO-SAFETY-1`: no production `unsafe`, cgo, or `go:linkname`
- minimal pinned dependencies with automated update review
- README, LICENSE, SECURITY, CONTRIBUTING, CODE_OF_CONDUCT, `CHANGELOG.md`,
  THIRD_PARTY_NOTICES, and release documentation
- strict changelog maintenance for every implementation change
- semantic versioning and reproducible signed/tagged release automation

## Execution Plan

1. Specify lifecycle states, ownership, package boundaries, errors, and limits.
2. Implement lifecycle and deterministic test utilities.
3. Implement HTTP, health, and optional integration packages.
4. Build complete examples and adoption documentation.
5. Prove race freedom, bounded shutdown, no leaks, and benchmark baselines.
6. Complete security review, compatibility review, CI, and `v1` release.

## Acceptance Criteria

The goal is complete only when every documented scenario has executable
examples, all public behavior has meaningful 100% coverage, all safety and CI
gates pass, lifecycle/resource guarantees are adversarially proven, and a new
service can adopt the module without hidden framework coupling.
