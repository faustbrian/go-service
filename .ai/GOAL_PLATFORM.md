# Goal: Evolve `service` Into A Cohesive Public Service Platform

## Mission

Evolve `service` from a collection of independently useful runtime primitives
into a cohesive, public, production-grade platform for constructing and
operating Go services consistently.

The platform must remove repeated non-business application construction from
Track, Postal, Location, and future services while preserving explicit Go
composition. It must provide the narrow set of conventions and runtime
capabilities that every independently deployed service needs without becoming
a general web framework, dependency-injection container, deployment system, or
business architecture.

The result should make the correct service setup substantially smaller than an
ad hoc implementation while keeping every dependency, lifecycle transition,
middleware, resource owner, and failure mode visible to the caller.

This goal is additive to `.ai/GOAL.md`, `.ai/GOAL_HARDEN.md`, and
`.ai/GOAL_POLISH.md`. Those files must not be rewritten to retroactively claim
this platform work. Requirements in this file must be implemented, verified,
documented, and recorded independently.

## Naming Decision

The module and public product name must remain `service`.

`service` is the precise term for the package's responsibility: constructing
and operating SOA and microservice processes. `platform` describes the role the
package plays across the organization, but it is too broad for the import path
and could incorrectly imply ownership of infrastructure provisioning,
deployment, developer portals, databases, networking, or the complete `golib`
ecosystem.

Do not introduce a competing `platform` package, repository, facade, or module.
Do not rename `service` to `platform`.

Documentation may describe `service` as a "service platform" or "microservice
runtime platform" where that wording clarifies its purpose.

## Product Position

`service` must be:

- a public open source module
- a narrow platform for APIs, RPC servers, ingesters, processors, workers,
  scheduled workloads, migrations, and mixed-role services
- standard-library-first and based on `context`, `net/http`, `log/slog`, and
  explicit interfaces
- usable as individual subpackages or as one cohesive bootstrap path
- suitable for Kubernetes without being coupled to Kubernetes
- infrastructure-neutral at its core
- explicit about construction, ownership, startup, readiness, draining,
  cancellation, shutdown, and cleanup
- deterministic and testable without signals, real clocks, or real network
  dependencies where test seams are appropriate
- compatible with caller-owned routers, protocol handlers, loggers, telemetry
  providers, database pools, queue clients, schedulers, and configuration
- strict enough that independently developed services behave consistently

`service` must not attempt to recreate Laravel's service container, facades,
automatic controller injection, model binding, sessions, CSRF handling,
templates, application globals, runtime package discovery, or hidden provider
boot process.

The desired outcome is Laravel/Lumen-like consistency for the narrow
non-business runtime surface, not Laravel-like magic.

## Reference Applications And Discovery

Before fixing the platform API, inspect the current Go implementations and
active migration work for Track, Postal, and Location. Inventory repeated and
divergent application construction, including:

- `main` functions and command/role dispatch
- service naming and build metadata
- configuration loading and validation
- logger construction and redaction
- telemetry initialization and shutdown
- HTTP server construction
- router and middleware composition
- JSON-RPC and API handler mounting
- liveness, startup, and readiness endpoints
- PostgreSQL, Valkey, Kafka, queue, and scheduler lifecycle
- signal handling and graceful shutdown
- worker and ingester supervision
- migration execution
- startup rollback and cleanup
- exit-code mapping
- tests, local development, and container entrypoints

Create a comparison matrix that distinguishes:

1. behavior that should be identical in every service;
2. behavior that should be configurable through a typed option;
3. behavior that belongs in an optional adapter;
4. behavior that must remain application-owned;
5. accidental divergence that should be removed; and
6. genuinely different service requirements that must not be forced behind one
   abstraction.

Do not copy one application's current bootstrap blindly. Treat all current
implementations as evidence and consumers, not as unquestionable design
authority.

## Primary Developer Experience

The package must offer a cohesive high-level construction path in addition to
the existing low-level subpackages.

A new service should be able to:

1. define its identity and build information;
2. declare supported process roles or commands;
3. provide typed configuration loading and validation;
4. provide caller-owned logging and telemetry facilities;
5. register lifecycle-managed infrastructure components;
6. mount business-owned HTTP, RPC, worker, scheduler, migration, or ingestion
   behavior;
7. run under a caller-provided or signal-derived context; and
8. receive deterministic startup, readiness, draining, shutdown, and exit-code
   behavior.

The final API does not have to use these exact identifiers, but its adoption
surface should be no more complicated than the following conceptual shape:

```go
func main() {
    os.Exit(service.Main(service.Definition{
        Identity: service.Identity{
            Name:    "postal",
            Version: build.Version,
            Commit:  build.Commit,
        },
        Commands: service.Commands{
            Serve:    buildServer,
            Worker:   buildWorker,
            Schedule: buildScheduler,
            Migrate:  buildMigrator,
        },
    }))
}
```

Construction callbacks must receive explicit typed dependencies or a narrowly
scoped construction context. They must not receive a mutable service locator,
generic `map[string]any`, global registry, or reflection-driven container.

The high-level path must compose the lower-level packages rather than create a
second lifecycle, HTTP, health, or supervision implementation.

Advanced callers must remain able to use `service`, `serverhttp`,
`healthhttp`, and other subpackages directly.

## Command And Process Role Model

Standardize a one-binary command model suitable for independently deployed
Kubernetes workloads.

The first platform contract must support:

- `serve`
- `worker`
- `schedule`
- `migrate`

It should be possible to add explicitly registered application commands
without modifying the platform.

The command model must define:

- argument parsing and unknown-command behavior
- help and version output
- configuration loading boundaries
- which facilities initialize for each command
- signal behavior
- exit codes
- startup and shutdown timeouts
- whether health serving is applicable
- error rendering without secret disclosure
- deterministic command tests without replacing `os.Args` globally

Do not start PostgreSQL, Valkey, telemetry, HTTP listeners, or any other
facility merely because it was registered. Each role must initialize only the
dependencies it declares.

Do not combine unrelated roles in one process by default. Mixed-role processes
may be supported explicitly for valid workloads, but the platform must preserve
independent ownership and failure semantics.

Integrate with the owned command and prompts packages where they provide a
stable benefit, but do not make interactive prompts or a large command
framework mandatory for production service binaries.

## Service Identity And Build Metadata

Define one typed service identity contract containing, where available:

- stable service name
- process role
- semantic version
- source revision
- build timestamp
- Go version
- deployment environment
- instance identifier

Identity must be available consistently to:

- structured logs
- traces and metrics
- health responses where safe
- user-agent construction for outbound clients
- diagnostics
- error reports

High-cardinality or sensitive deployment values must not become metric labels.
Missing build-time values must have documented deterministic behavior and must
not cause startup failure unless explicitly required by policy.

## Configuration Contract

`service` must orchestrate configuration without reimplementing the `config`
module.

The supported operating model is:

- local development loads optional `.env` files and environment variables;
- non-local deployments receive environment variables or mounted values from
  Infisical through Kubernetes;
- configuration is decoded into an application-owned typed struct;
- validation completes before dependent components start;
- configuration errors fail startup with safe, actionable messages.

The platform must define and document:

- source precedence
- local `.env` discovery and opt-in behavior
- required versus optional values
- typed decoding
- validation timing
- secret redaction
- unknown-key policy where supported
- role-specific configuration
- test overrides without global environment races
- reload behavior and non-reloadable fields

The core must not:

- contain AWS Secrets Manager assumptions;
- import an AWS SDK;
- require direct Infisical API access;
- assume that every secret can safely change at runtime; or
- silently reload credentials without coordinating dependent resources.

Infisical is primarily a deployment-layer secret delivery mechanism. A direct
Infisical provider or dynamic secret refresher may be supported as an optional
`config` integration, but it must not be required by `service`.

If dynamic configuration or secret refresh is supported, require:

- explicit opt-in;
- versioned snapshots;
- validation before publication;
- atomic publication;
- subscriber backpressure policy;
- retry and stale-value policy;
- refresh observability;
- rollback to the last valid snapshot;
- defined behavior for database pools, HTTP clients, queue clients, and other
  resources that cannot change credentials in place; and
- deterministic tests for races, partial refresh, invalid values, and
  shutdown.

## Kubernetes Runtime Contract

The reference deployment target is UpCloud Kubernetes provisioned with
OpenTofu. The package must remain portable to other Kubernetes providers and
non-Kubernetes environments.

OpenTofu modules, Kubernetes manifests, Helm charts, workload identity,
networking, ingress, autoscaling, and secret synchronization do not belong in
the Go package.

The package must expose runtime behavior that Kubernetes can consume:

- deterministic startup
- startup probe
- liveness probe
- readiness probe
- readiness withdrawal before shutdown
- graceful connection draining
- bounded termination
- signal handling for `SIGTERM` and `SIGINT`
- termination timing that can be aligned with
  `terminationGracePeriodSeconds`
- dependency-safe startup and shutdown ordering

Document complete Kubernetes examples for API/RPC, worker, scheduler, and
migration Job workloads. Examples must show how probe timeouts and termination
budgets relate to application timeouts rather than presenting arbitrary
numbers.

## Canonical Health Endpoints

Standardize exactly these canonical paths:

- `/livez`
- `/startupz`
- `/readyz`

Do not make `/health/live`, `/health/startup`, `/health/ready`, `/healthz`, or
application-specific variants the primary contract.

Legacy aliases may be offered only through explicit compatibility options.
They must not be registered silently, and documentation must recommend the
canonical paths.

### `/livez`

`/livez` answers whether the process is alive and able to execute its runtime
loop.

It must:

- avoid external dependency checks;
- remain fast and allocation-conscious;
- not fail because PostgreSQL, Valkey, Kafka, or another dependency is
  unavailable;
- return failure only when process-local state proves the process should be
  restarted; and
- expose no sensitive diagnostics.

### `/startupz`

`/startupz` answers whether required initialization for the selected role has
completed successfully.

It must:

- remain unsuccessful until required startup completes;
- distinguish still-starting from terminal startup failure internally;
- become stable after successful startup unless the process restarts;
- not be reused as a general dependency health endpoint; and
- provide a bounded, machine-readable response.

### `/readyz`

`/readyz` answers whether the instance should receive new work.

It must:

- remain unsuccessful before startup completes;
- become unsuccessful before draining begins;
- check only dependencies required to accept and correctly process new work;
- execute dependency checks with explicit timeout, concurrency, and result
  bounds;
- avoid exposing dependency addresses, credentials, raw errors, or stack
  traces; and
- define recovery behavior after a transient dependency failure.

The platform must document when a dependency belongs in readiness and when it
does not. Readiness must not become a recursive check of every downstream
system.

## HTTP Runtime

Keep `net/http` as the public runtime contract.

The high-level platform must compose `serverhttp` and expose secure,
production-appropriate defaults for:

- read-header timeout
- read timeout
- write timeout
- idle timeout
- maximum headers
- request body limits
- graceful shutdown timeout
- base context and connection context
- panic containment
- safe error responses
- request logging
- correlation
- trace context
- optional compression
- trusted proxy handling where explicitly configured

Every default must be documented. Every timeout and limit must be independently
overridable. Zero and negative values must have explicit semantics and must not
accidentally disable a safety boundary.

The package must preserve relevant `http.ResponseWriter` capabilities such as
`Flusher`, `Hijacker`, `Pusher`, and `io.ReaderFrom` when middleware wrapping
claims compatibility.

Streaming, server-sent events, WebSockets, uploads, downloads, and long-running
RPC calls must have documented timeout and shutdown behavior even when their
protocol implementation remains application-owned.

## Mandatory Correlation Middleware

Every HTTP request handled through the cohesive platform path must receive a
correlation identifier as early as possible.

The canonical header is:

```text
X-Correlation-ID
```

The middleware contract must:

1. inspect the inbound header before invoking downstream middleware;
2. accept it only when it satisfies documented length, character, and format
   constraints;
3. generate a cryptographically unpredictable identifier when it is absent or
   invalid;
4. place the identifier in `context.Context` using a collision-safe private
   key;
5. set `X-Correlation-ID` on the response before invoking the next handler;
6. expose typed retrieval and propagation helpers;
7. make the value available to logs, traces, errors, outbound clients, RPC
   calls, queue messages, jobs, and events through explicit adapters;
8. behave correctly for panics, early errors, redirects, streaming, upgrades,
   and partially written responses; and
9. avoid unbounded cardinality and log/header injection.

The middleware must document the distinction between:

- a correlation ID, which connects related work across boundaries;
- a request ID, which uniquely identifies one transport request;
- an idempotency key, which controls duplicate side effects; and
- W3C `traceparent`, which carries distributed tracing context.

Do not derive idempotency behavior from the correlation ID. Do not replace
W3C trace propagation with `X-Correlation-ID`.

Decide explicitly whether a valid inbound correlation ID is preserved as-is or
whether a separate request ID is always generated. The decision must be
documented, tested, and applied consistently across all service examples.

Provide adapters for the owned HTTP client, JSON-RPC, queue, Kafka, logging,
and telemetry packages where those packages support metadata propagation.
Adapters must preserve dependency direction and avoid cycles.

## Middleware Pipeline

Define one documented default middleware order. The exact order must be
justified through observable behavior and security constraints.

At minimum, evaluate and place:

- panic recovery
- correlation ID
- W3C trace extraction
- trusted proxy and client address resolution
- request body limits
- decompression
- authentication
- authorization
- rate limiting
- circuit breaking where applicable
- request logging
- metrics
- response security headers
- compression

The platform must distinguish mandatory safety middleware, recommended
middleware, and application-selected middleware.

Ordering must remain inspectable and overrideable through explicit composition.
Do not use hidden registration, `init`, mutable package globals, or priority
integers whose final order is difficult to determine.

Middleware from `http-middleware`, `authentication`, `authorization`,
`rate-limit`, `telemetry`, and other owned modules should be composed rather
than reimplemented.

## Logging And Better Stack

The core logging contract is `*slog.Logger` or a narrowly justified
standard-library-compatible interface.

The platform must:

- accept caller-owned loggers;
- enrich logs consistently with service identity, process role, correlation
  ID, trace ID, and safe request metadata;
- provide explicit redaction integration;
- define structured error attributes;
- avoid logging request bodies, credentials, tokens, cookies, authorization
  headers, connection strings, or raw configuration by default;
- flush or close caller-provided facilities only when ownership is explicitly
  transferred; and
- behave predictably when logging is disabled or initialization fails.

Better Stack is the intended production log and telemetry destination, but the
core must not become Better Stack-specific.

Provide or document an optional Better Stack integration through the owned
logging and telemetry packages. Prefer standard OTLP and supported structured
log transport over a vendor-specific runtime abstraction when equivalent.

Daily local file rotation, stack/fan-out handlers, console output, and remote
delivery belong in the logging package. `service` should orchestrate lifecycle
and enrichment, not implement logging backends.

## OpenTelemetry And Telemetry

Use the owned `telemetry` package and OpenTelemetry contracts for production
observability.

The platform must support caller-owned:

- trace providers
- meter providers
- propagators
- OTLP exporters
- resource attributes
- sampling configuration

It must standardize:

- service identity resource attributes
- process role attributes
- inbound and outbound context propagation
- HTTP server spans
- request duration and status metrics
- lifecycle events
- startup, readiness, and shutdown measurements
- dependency initialization failures
- worker and scheduler execution boundaries

It must not:

- initialize global OpenTelemetry providers implicitly;
- force one exporter;
- force Better Stack;
- use correlation IDs, request paths with unbounded parameters, customer IDs,
  or arbitrary errors as metric labels; or
- create duplicate spans when router or protocol middleware already
  instruments the request.

Telemetry startup failure policy must be explicit and configurable. Shutdown
must be bounded and ordered so final telemetry can flush without delaying
termination indefinitely.

## Lifecycle Components And Dependency Adapters

Define a small lifecycle component contract that can represent caller-owned
infrastructure without hiding its concrete type.

The platform must support deterministic composition of:

- PostgreSQL pools
- Valkey clients
- Kafka producers and consumers
- queue producers and workers
- schedulers
- HTTP clients
- RPC servers
- telemetry providers
- log sinks requiring flush
- custom application components

Adapters should live in optional subpackages or the owning library where
necessary to avoid forcing dependencies into the core module.

Every adapter must define:

- construction ownership
- startup behavior
- readiness behavior
- draining behavior
- shutdown behavior
- timeout source
- retry responsibility
- error classification
- whether cleanup is safe after partial startup
- whether repeated shutdown is safe
- whether the underlying client may be shared

Do not wrap mature clients merely to hide their APIs. Adapters should connect
their lifecycle to `service`, not create inferior client abstractions.

## Startup, Rollback, Supervision, And Shutdown

The cohesive platform path must preserve and strengthen the existing lifecycle
guarantees.

Prove:

- deterministic startup ordering
- explicit parallel startup only where dependency independence is declared
- rollback of every successfully started component after later startup failure
- reverse-order cleanup
- aggregation without loss of primary and cleanup errors
- cancellation causes
- readiness withdrawal before accepting shutdown work
- HTTP draining before dependent resources close
- worker intake stopping before in-flight work is cancelled
- bounded completion of in-flight work
- forced termination after the configured budget
- no orphan goroutines, timers, listeners, transactions, or connections
- repeatable and concurrency-safe shutdown
- correct behavior for repeated signals
- correct behavior when a component ignores cancellation
- correct exit-code mapping for configuration, startup, runtime, signal, and
  shutdown failures

Every goroutine started by the platform must have a named owner, cancellation
path, and join path.

## Router, RPC, Authentication, And Authorization Boundaries

`service` must not own routing or business protocols.

It must compose cleanly with:

- the owned router package
- plain `http.ServeMux`
- JSON-RPC
- JSON:API
- OpenAPI-generated handlers
- authentication
- authorization
- HTTP middleware

The platform may provide mounting helpers only when they reduce repeated
runtime wiring without obscuring the underlying `http.Handler`.

Business routes, RPC methods, request validation, authorization decisions,
domain errors, response schemas, and protocol-specific compatibility remain
application-owned or owned by their protocol packages.

No controller discovery, route scanning, reflection registration, model
binding, service container, or implicit middleware activation is allowed.

## PostgreSQL, Valkey, Queue, Kafka, And Scheduler Boundaries

The platform should provide lifecycle and observability composition for owned
infrastructure packages without merging their APIs into `service`.

### PostgreSQL

- Accept caller-created pools or explicit constructors.
- Support startup ping policy without making every transient failure fatal by
  accident.
- Make readiness inclusion explicit.
- Close pools after HTTP, RPC, worker, and scheduler work has drained.
- Do not own SQL queries, transactions, migrations, or schema policy.

### Valkey

- Support the dedicated Valkey client path used in production.
- Do not assume Redis-specific server behavior.
- Make readiness inclusion explicit.
- Do not close shared clients unexpectedly.
- Keep cache and queue semantics in their owning packages.

### Queue And Kafka

- Stop intake before waiting for in-flight work.
- Define acknowledgement behavior when shutdown deadlines expire.
- Propagate correlation and trace metadata explicitly.
- Keep retry, dead-letter, ordering, partitioning, and delivery semantics in
  the queue or Kafka packages.
- Do not supervise Kubernetes replicas or replace the queue control plane.

### Scheduler

- Coordinate leader-only and non-overlapping execution through the scheduler
  package.
- Stop new scheduling before waiting for active executions.
- Keep schedules and business commands application-owned.
- Do not turn the service runtime into a cron expression engine.

## Migrations

The `migrate` role must provide consistent process lifecycle and exit behavior
while leaving migration semantics to the migration package and application.

It must support:

- one-shot execution
- typed configuration
- bounded database connection
- structured logging and telemetry
- exact non-zero failure status
- no HTTP server unless explicitly requested
- no unrelated cache, queue, Kafka, or scheduler initialization
- safe cleanup after success or failure

The platform must not become a migration engine or track database migration
state.

## Security Requirements

Threat-model the platform as an internet-facing and internally trusted service
runtime.

At minimum, address:

- malformed and oversized headers
- malformed or hostile correlation IDs
- header and log injection
- slowloris clients
- oversized and compressed request bodies
- decompression bombs
- panic and error disclosure
- spoofed forwarding headers
- untrusted proxy configuration
- authentication middleware bypass caused by ordering
- authorization middleware bypass caused by mounting
- trace and baggage abuse
- high-cardinality telemetry
- unbounded health checks
- signal storms
- shutdown denial of service
- configuration and secret disclosure
- accidental debug mode in production
- unsafe default listeners
- TLS termination assumptions

Security-sensitive behavior must default safely. Unsafe compatibility behavior
must require explicit opt-in and prominent documentation.

Run CodeQL, `govulncheck`, static analysis, fuzzing, race detection, and hostile
integration tests through the repository's canonical gates. NilAway remains
advisory but findings must be reviewed.

## Error Model

Define typed errors and stable classification for:

- invalid service definitions
- configuration failure
- command usage failure
- component construction failure
- startup failure
- startup rollback failure
- runtime component failure
- readiness failure
- drain failure
- shutdown timeout
- shutdown cleanup failure

Errors must:

- preserve causes through `errors.Is` and `errors.As`;
- retain component and lifecycle phase without embedding secrets;
- distinguish user-correctable configuration from transient dependency
  failure;
- support deterministic exit-code mapping;
- avoid exposing internal details in HTTP responses; and
- remain useful in logs and telemetry without requiring string parsing.

## Testability And `servicetest`

Expand `servicetest` only where helpers prove real platform contracts.

Provide deterministic facilities for:

- fake signals
- fake clocks and timers where timing is owned
- lifecycle event recording
- component startup and shutdown barriers
- startup failure injection
- readiness transitions
- HTTP probe assertions
- correlation propagation
- logger capture with redaction assertions
- telemetry capture
- command invocation
- exit-code assertions
- leak detection

Test helpers must not encourage source-text assertions, mock choreography, or
implementation coupling. They must remain optional and must not introduce
production dependencies.

## Required Verification

All production code introduced or changed by this goal must satisfy the
repository-wide maximum-strictness contract.

Required evidence includes:

- meaningful exact 100% statement coverage for every production package
- exact 100% mutation efficacy and mutant coverage for every viable mutant
- deterministic unit tests
- real-listener HTTP integration tests
- subprocess tests for real signal behavior where safe
- race-enabled tests
- goroutine, timer, listener, and connection leak tests
- fuzzing with retained regression corpora
- hostile-input and resource-bound tests
- examples compiled and executed in CI
- clean-consumer tests importing only documented package paths
- compatibility tests for the low-level API and cohesive platform API
- Linux execution matching the deployment environment

Coverage must not be obtained with empty assertions, impossible branches,
test-only production hooks, ignored production files, or aggregation that
masks uncovered packages.

Mutation exclusions must follow repository policy and may not be used to lower
the target.

## Performance And Resource Goals

The cohesive platform must preserve the resource advantages motivating the Go
migration.

Build reproducible, behavior-matched benchmarks for:

- process startup
- idle resident memory
- binary size
- HTTP request latency
- JSON-RPC request latency
- throughput under concurrency
- allocations per request
- probe latency and allocations
- correlation middleware overhead
- logging-disabled and logging-enabled paths
- tracing-disabled and tracing-enabled paths
- graceful shutdown
- worker dispatch and supervision overhead

Compare:

- plain `net/http`
- low-level `service` composition
- cohesive `service` platform composition
- Chi
- Gin
- Echo
- Fiber/fasthttp as a separately disclosed incompatible runtime

Comparisons must implement equivalent routes, middleware, JSON work, logging,
telemetry state, body limits, timeouts, health behavior, and shutdown behavior.
Do not publish rankings based on intentionally weaker competitor behavior.

Define explicit regression budgets before optimization. The cohesive layer must
not add material latency, allocations, startup time, or idle memory relative to
the low-level owned stack without a documented and accepted tradeoff.

Use realistic Postal search, Track ingestion/RPC, and Location lookup workload
shapes in addition to trivial handlers, while keeping business implementations
outside this module.

## Documentation Deliverables

Create or update user-facing documentation so a new team can construct a
service without reading implementation source.

Required documentation:

1. platform overview and design philosophy;
2. five-minute quick start;
3. complete public API reference;
4. choosing low-level subpackages versus cohesive platform construction;
5. command and role model;
6. lifecycle and ownership model;
7. canonical health endpoint contract;
8. correlation, request ID, idempotency, and trace propagation;
9. middleware ordering and customization;
10. local `.env` configuration;
11. Infisical-delivered Kubernetes configuration;
12. logging and Better Stack integration;
13. OpenTelemetry and OTLP integration;
14. PostgreSQL and Valkey lifecycle;
15. queue, Kafka, scheduler, and migration role integration;
16. Kubernetes deployment and graceful termination;
17. security and threat model;
18. performance and benchmark methodology;
19. adoption from ad hoc Go bootstrap code;
20. migration guidance for Track, Postal, and Location;
21. testing with `servicetest`;
22. troubleshooting;
23. FAQ;
24. intentional limitations and non-goals; and
25. versioning and compatibility policy.

Provide complete runnable examples for:

- HTTP API
- JSON-RPC service
- ingester
- processor/worker
- scheduler
- migration command
- mixed command binary
- PostgreSQL-backed service
- Valkey-backed service
- Kafka consumer
- queue worker
- Better Stack/OTLP observability
- correlation propagation across HTTP and queue boundaries

Every example must use the recommended API and must be checked in CI.

## Package Boundary Investigation

Determine the smallest cohesive package shape before implementation.

Candidate responsibilities include:

- high-level bootstrap in `service` or a `bootstrap` subpackage
- existing lifecycle in `service`
- existing HTTP runtime in `serverhttp`
- existing probes in `healthhttp`
- optional dependency adapters in focused subpackages
- test support in `servicetest`

Do not create deeply nested package hierarchies merely to mirror a conceptual
diagram. Go package names and import paths must remain concise and clear.

Avoid cycles between `service` and:

- `config`
- `log`
- `telemetry`
- `http-client`
- `http-middleware`
- `router`
- `authentication`
- `authorization`
- `postgres`
- `cache`
- `queue`
- `kafka`
- `scheduler`
- `migrate`

Prefer integration adapters in the higher-level consumer or owning package when
placing them in `service` would reverse dependency direction.

Document the final dependency graph and prove it through architecture tests.

## Compatibility And Adoption

Preserve the usefulness of existing low-level packages.

Before changing an exported API:

- inventory repository consumers and examples;
- determine whether an additive path is sufficient;
- document migration impact;
- add compile-time compatibility fixtures;
- update API baselines;
- apply semantic versioning correctly; and
- avoid retaining a defective abstraction solely for source compatibility when
  the repository has not published a stable release.

Build migration spikes for Track, Postal, and Location that replace only their
generic bootstrap and retain their business behavior. These spikes must prove:

- reduced application-local runtime code;
- no loss of explicit dependency visibility;
- consistent commands, probes, middleware, logs, telemetry, and shutdown;
- no new domain dependency on `service`;
- no cross-service business coupling; and
- no material performance or resource regression.

Do not turn this goal into full service migrations. Use bounded integration
spikes or fixtures sufficient to validate the platform contract.

## Explicit Non-Goals

This goal must not implement or own:

- application domain architecture
- bounded-context layout
- business use cases
- business authorization policy
- database schemas or queries
- migrations or migration state
- queue payload schemas
- Kafka topic design
- retry or dead-letter policy
- schedules
- vendor clients
- API or RPC schemas
- request validation rules
- JSON:API, JSON-RPC, OpenAPI, or OpenRPC protocol behavior
- router implementation
- authentication implementation
- authorization engine
- rate limiter
- circuit breaker
- cache implementation
- object storage
- Kubernetes manifests or controllers
- OpenTofu
- Infisical server or operator behavior
- Better Stack backend behavior
- service discovery
- API gateway
- sidecar management
- autoscaling
- process replica supervision
- dependency injection container
- reflection-based registration
- mutable global application state

## Implementation Phases

### Phase 1: Consumer Inventory And Contract

- Audit Track, Postal, Location, and existing `service` consumers.
- Produce the repeated/divergent construction matrix.
- Define the platform boundary and dependency graph.
- Specify command, lifecycle, health, correlation, configuration,
  observability, error, and exit-code contracts.
- Establish performance and resource baselines.
- Record compatibility constraints.

No production API should be added until these contracts are reviewable.

### Phase 2: Cohesive Bootstrap

- Implement the smallest high-level construction API.
- Reuse existing lifecycle, HTTP, health, integration, and test packages.
- Implement role-specific dependency construction.
- Add deterministic command execution and exit-code mapping.
- Preserve direct low-level usage.

### Phase 3: Canonical Runtime Behavior

- Standardize `/livez`, `/startupz`, and `/readyz`.
- Implement mandatory correlation behavior for the cohesive HTTP path.
- Define and enforce middleware order.
- Complete startup, rollback, draining, and shutdown behavior.
- Add typed identity and build metadata.

### Phase 4: Optional Integrations

- Compose typed configuration and local `.env` behavior.
- Add optional logging and Better Stack guidance.
- Add optional OpenTelemetry/OTLP integration.
- Add lifecycle adapters or examples for PostgreSQL, Valkey, Kafka, queue,
  scheduler, migrations, router, RPC, authentication, and authorization.
- Resolve dependency direction without cycles.

### Phase 5: Consumer Validation

- Build bounded Track, Postal, and Location bootstrap migrations or fixtures.
- Compare code volume, behavior, startup, memory, latency, allocations, and
  shutdown.
- Remove platform gaps exposed by more than one consumer.
- Reject application-specific features from the public core.

### Phase 6: Hardening

- Complete threat modeling.
- Achieve meaningful exact coverage and mutation requirements.
- Run race, fuzz, leak, stress, compatibility, security, and clean-consumer
  gates.
- Validate Linux and Kubernetes lifecycle behavior.
- Review all defaults, limits, timeouts, ownership, and failure semantics.

### Phase 7: Documentation And Release Readiness

- Complete all documentation and runnable examples.
- Update API baselines and changelog.
- Publish benchmark methodology and results.
- Run the complete affected repository verification.
- Produce a release-readiness report with no stronger claims than the
  executable evidence.

## Required Deliverables

1. Track/Postal/Location construction comparison matrix.
2. Public platform boundary and non-goal decision record.
3. Package dependency graph.
4. Command and role specification.
5. Lifecycle and resource ownership specification.
6. Health endpoint specification.
7. Correlation and propagation specification.
8. Middleware order specification.
9. Configuration and Infisical deployment specification.
10. Logging, Better Stack, and telemetry specification.
11. Typed error and exit-code specification.
12. Cohesive bootstrap implementation.
13. Optional integration adapters or documented composition.
14. Deterministic `servicetest` support.
15. Track, Postal, and Location validation spikes.
16. Security threat model and hardening report.
17. Equivalent-behavior performance comparison.
18. Complete adoption, API, operations, and troubleshooting documentation.
19. Updated changelog and API compatibility baseline.
20. Final evidence matrix mapping every platform promise to implementation,
    tests, documentation, and current verification.

## Release Blockers

The platform work is not ready when any of the following remains:

- hidden initialization or global mutable state
- service locator or reflection-driven dependency injection
- ambiguous ownership or cleanup
- unbounded startup, health, request, drain, or shutdown work
- a goroutine without cancellation and join behavior
- inconsistent health paths or semantics
- missing correlation on a platform-managed HTTP request
- malformed inbound correlation values reaching logs or response headers
- confusion between correlation, tracing, request identity, and idempotency
- secret disclosure
- AWS-specific runtime coupling
- mandatory direct Infisical coupling
- mandatory Better Stack coupling
- dependency cycles
- business behavior moved into `service`
- initialization of dependencies unused by the selected role
- application-specific abstractions justified by only one consumer
- undocumented defaults or middleware order
- material performance or resource regression
- less than exact meaningful 100% coverage
- less than exact 100% mutation efficacy or mutant coverage
- race, leak, fuzz, security, compatibility, docs, or CI failures
- examples that do not compile
- unresolved high- or medium-severity review findings
- stale evidence after affected inputs changed

## Completion Criteria

This goal is complete only when:

- Track, Postal, and Location can use the same public construction model for
  their generic runtime without sharing business logic;
- a new service can implement `serve`, `worker`, `schedule`, and `migrate`
  roles with minimal application-local bootstrap;
- all dependencies and lifecycle ownership remain explicit;
- canonical `/livez`, `/startupz`, and `/readyz` behavior is identical across
  services;
- every platform-managed HTTP request has a safe propagated
  `X-Correlation-ID`;
- local `.env`, Infisical-delivered production configuration, Better Stack,
  and OpenTelemetry adoption are completely documented without coupling the
  core to one infrastructure vendor;
- PostgreSQL, Valkey, Kafka, queue, scheduler, migrations, router, RPC,
  authentication, and authorization compose without dependency cycles or
  duplicated implementations;
- migration spikes demonstrate substantially less repeated runtime code;
- equivalent-behavior benchmarks demonstrate no material platform overhead;
- every public contract has meaningful exact coverage and mutation evidence;
- all repository quality, security, compatibility, documentation, and release
  gates pass; and
- the final implementation remains recognizably explicit Go rather than a
  hidden framework runtime.
