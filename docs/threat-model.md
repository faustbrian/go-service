# Service threat model

**Model version:** 1.0

**Reviewed:** 2026-10-01

**Owner:** `go-service` maintainers

**Applies to:** root `github.com/faustbrian/go-service` source prepared for
v1.1.2, including `healthhttp`, `serverhttp`, `integration`, and `servicetest`.
The eight non-releasable modules in `modules.json` exercise maintained
benchmark, compatibility, adoption, and reference compositions; they are not
independently supported production releases. Published v1.1.1 retains its
immutable source. Publication and application deployment are separate steps.

## Assets and boundaries

Protect service availability, ordered resource ownership, admitted-work
completion, maintenance state and bypass credentials, diagnostic privacy, and
the integrity of public module inputs.

| Boundary | Inputs and ownership | Package controls |
| --- | --- | --- |
| Construction and lifecycle | Application-owned definitions, component hooks, tasks, configuration, and listeners | Inert construction, validated runtime identities, ordered startup, reverse cleanup, classified errors, explicit contexts and task caps |
| HTTP and probes | Request headers, bodies, correlation metadata, cancellation, and dependency results | Finite default request/body/header/shutdown limits, panic containment, fresh request identity, bounded probe scheduling and global check concurrency, safe binary probe results |
| Maintenance persistence | File bytes or caller-owned shared-store snapshots | Bounded file reads, complete JSON validation, duration admission before conversion, atomic file publication, empty rejected snapshots, validated shared-store results |
| Admission and bypass | Maintenance state, request path, bypass cookie, trusted policy metadata | Explicit maintenance admission, domain-separated credential digest, constant-time cookie comparison, no secret in status or runtime events |
| Diagnostics | Callback errors, panic values, operational identities, logging handlers and observers | Default lifecycle/maintenance categories, retained programmatic causes, safe HTTP errors, bounded runtime event vocabulary |
| Dependencies and release | Public module versions, workflow/tool pins, generated API baseline and signed artifacts | Pinned shared CI, API checks, required job aggregation, applicable scanner and public-consumer release gates |

TLS, proxy trust, authentication, authorization, network policy, business
handlers, database/queue operations, and deployment privileges remain owned by
the application. Correlation and trusted resilience priority are not identity
or authorization evidence. The runtime does not implicitly open those services.

## Required behavior

- Imports and construction do not start hidden background work. Started tasks
  have service cancellation and explicit joining/cleanup ownership.
- Components transfer ownership only after successful startup. Admission closes
  before draining, and acquired components stop in reverse order.
- Active supervised tasks default to 64 and cannot exceed the configured hard
  ceiling of 4,096. Probe counts and concurrency have independent hard ceilings.
  Trusted construction lists are not an untrusted request-input boundary.
- HTTP defaults bound header and body retention and request/shutdown timeouts.
  Explicitly disabling a timeout transfers that limit to deployment policy.
- File maintenance reads consume at most 8,193 bytes. The admitted snapshot is
  at most 8,192 bytes; persisted durations must be zero through 604,800 seconds.
  Validation precedes duration conversion. Valid zero and seven-day values
  retain their behavior, and rejected loads return an empty state.
- Context is checked before and after file reads. Shared storage and callbacks
  must implement their own context-aware work; this is not forced preemption.
- Maintenance status omits bypass credentials. Runtime events and default
  recovered-panic responses do not expose private callback failure details.
  Explicit unwrapping and caller-owned logging are separate diagnostic paths.

## Accepted conditional risks

| Risk | Owner | Rationale | Mitigation | Review trigger |
| --- | --- | --- | --- | --- |
| Trusted hooks, health checks, observers, logger handlers, or response handlers may ignore cancellation or block. | Application owners | Go cannot safely preempt caller code; lifecycle and callback ownership remain explicit. | Bound callback work, honor contexts, use bounded nonblocking telemetry handoffs, and supervise the process with a hard termination budget. Noncooperative probe work retains its concurrency permit until it returns. | A callback exceeds its budget, a new exporter/hook is introduced, or execution ownership changes. |
| File operations may block beyond the context deadline; paths, directories, and symlinks are deployment-owned. | Deploying operators | Portable file APIs do not provide forced cancellation or a complete sandbox. | Use trusted regular files and protected directories, prohibit untrusted path selection, apply filesystem/process limits, and supervise termination. | A new filesystem or file type is used, paths become untrusted, or an operation outlives the supervisor budget. |
| The file adapter stores the bypass credential in plaintext; later refresh failure retains the last valid state. | Application owners and deploying operators | The credential is needed for bypass verification; retaining valid state avoids replacing policy with an invalid snapshot. | Restrict file and backup access, use the documented 0600 atomic publication, rotate credentials, monitor refresh failures, and use ingress maintenance for unavailable processes. | Secret distribution, backups, access policy, refresh behavior, or deployment topology changes. |
| Shared maintenance stores and concurrent deployments require coherent reads and atomic publication. | Application owners | The adapter cannot impose transactions on caller-owned databases or caches. | Implement the documented store contract; use the file adapter only on filesystems with coherent reads and atomic rename, and coordinate multi-instance deployment. | A shared backend or replication topology changes, or inconsistent maintenance state is observed. |
| TLS, proxy trust, authentication, authorization, ingress session/concurrency limits, and disabled HTTP timeouts are caller-controlled. | Deploying application owners | The library composes handlers and listeners, not an application security policy. | Authenticate before assigning trusted metadata, restrict management routes, configure transport/proxy policy, and provide finite ingress/capacity limits when selecting lower-level APIs. | A listener is exposed, a proxy/authentication policy changes, or a request limit is disabled. |
| Programmatic error causes, panic values, names, configuration and log attributes may contain application-private data. | Application owners | Classified errors preserve controlled diagnosis; trusted configuration is not automatically safe to publish. | Do not serialize unwrapped causes or panic values; use bounded, nonsecret operational identities, cap application-owned configuration lists/attributes, and redact caller-provided logs. | Configuration becomes remotely supplied, diagnostic serialization changes, or private data reaches telemetry. |
| A panic after an HTTP response commits cannot retract bytes already sent. | Application handler owners | HTTP has no rollback for a committed response. | Validate before committing, avoid private response content, treat partial responses as failures, and retain client retry/idempotency policy. | Streaming/commit behavior changes or a partial-response failure exposes protected data. |
| Dependency or maintainer compromise is not excluded by a passing scanner. | Repository maintainers and consuming application owners | Scanners and signatures establish narrower properties than runtime trust. | Review public pins, keep workflows/tools immutable, run applicable security and clean-consumer release checks, and follow the private reporting process. Preserve historical fixture pins rather than silently treating them as current production defaults. | A dependency, tool, release process, signer, or direct consumer changes, or compromise evidence appears. |

Every accepted risk is conditional on its stated owner and mitigation. None
permits default credential disclosure or knowingly accepting an unowned High
or Critical finding.

## Verification and release scope

Maintenance regression tests exercise public file-state loading, inclusive
duration boundaries, cancellation and empty rejection. The private reader seam
asserts consumed bytes and rejected partial snapshots without allocation or
exhaustion experiments. Existing lifecycle, HTTP, probe, and composition tests
remain the corresponding contract evidence, not proof of application policy.

Before the compatible root patch is published, require the complete reviewed
diff, affected root tests/vet/API checks, green exact-source required CI and
release checks, and an ordinary public consumer. Nested fixture changes or
unrelated module releases are not required by this ingress repair. Actual
deployment and filesystem termination behavior require application-owned
verification and are not implied by library tests.
