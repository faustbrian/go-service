# Platform observable contracts

This document freezes the Phase 1 command, lifecycle, HTTP, health,
correlation, configuration, and ownership contracts.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## Definition validation

Validation occurs before configuration or resource construction. A definition
is invalid when:

- identity name is blank or malformed;
- no command is registered;
- a command name is invalid or duplicated;
- a standard command is registered in `Custom`;
- command kind is unknown;
- a load or build callback is nil;
- a timeout or limit is outside its documented range;
- a caller listener conflicts with another owned listener; or
- middleware ownership is duplicated.

Definition errors are deterministic and have exit code 70.

## Command wire contract

The command grammar is:

```text
SERVICE COMMAND [COMMAND OPTIONS]
SERVICE help [COMMAND]
SERVICE version
SERVICE --help
SERVICE --version
```

No command prints secrets or raw configuration. Unknown commands and invalid
arguments write a single safe diagnostic and usage to stderr and return 2.
Help and version write to stdout and return 0.

Only the selected command loads configuration and constructs resources. Load
and build callbacks receive the bounded startup operation context.
Registration MUST NOT start work.

## Lifecycle state contract

```text
new
  |
  v
starting ---- failure ----> rolling-back ----> stopped
  |
  v
ready ---- drain request ----> draining ----> stopping ----> stopped
  |                                ^
  +---- runtime failure -----------+
```

Startup is sequential and context-aware acquisition is bounded by the
configured startup timeout. A component transfers ownership only after `Start`
returns nil. Failure rolls back every transferred component in reverse order.
The primary startup failure is preserved separately from cleanup failures.

Readiness becomes true only after required startup and task installation.
Drain first makes readiness false, then stops new business HTTP and worker
intake. In-flight work receives the configured drain budget. Infrastructure
closes only after business work joins.

Shutdown is repeatable and concurrency-safe. Every caller observes the same
terminal result. A caller abandoning its wait does not abandon the owned
shutdown operation.

Every platform goroutine has one owner, cancellation path, and join path.

## Signal contract

`Main` handles `SIGINT` and `SIGTERM`. The first signal starts graceful drain.
A second signal cancels remaining work immediately but still runs bounded
cleanup. Further signals do not create goroutines or duplicate cleanup.

`Execute` uses caller-supplied signal events and therefore supports
deterministic tests. Signal cancellation preserves a typed cause and maps to
130 or 143.

## Management server lifetime

For long-running roles:

1. bind the management listener;
2. expose liveness and unsuccessful startup/readiness;
3. construct and start required components;
4. install tasks and business HTTP;
5. mark startup and readiness successful;
6. withdraw readiness;
7. drain business work;
8. stop components;
9. retain management liveness through cleanup; and
10. stop the management server.

A bind or validation failure closes only resources whose ownership already
transferred. One-shot roles skip this sequence unless explicitly opted in.

## Probe HTTP contract

Canonical paths are exactly `/livez`, `/startupz`, and `/readyz`.

All probe handlers:

- accept `GET` and `HEAD`;
- return `Allow: GET, HEAD` with 405 for every other method;
- return `Content-Type: application/json`;
- return `Cache-Control: no-store`;
- return `X-Content-Type-Options: nosniff`;
- return `X-Correlation-ID` and `X-Request-ID`;
- return `X-Causation-ID` when a trusted immediate parent exists; and
- omit the body for `HEAD`.

The bounded body is:

```json
{"status":"ok","probe":"liveness"}
```

`status` is `ok` or `unavailable`; `probe` is `liveness`, `startup`, or
`readiness`; `checks` is absent unless safe details are explicitly enabled.
Probe bodies never contain raw errors, addresses, credentials, configuration,
stack traces, build paths, or dependency payloads.

Successful probes return 200. Unsuccessful startup and readiness return 503.
Liveness remains 200 while the management runtime executes; terminal runtime
failure terminates the process.

Startup state distinguishes still-starting from terminal failure internally.
Terminal startup failure proceeds to process exit instead of leaving a
permanently unavailable management server.

## Readiness checks

A dependency belongs in readiness only when the selected role cannot accept
and correctly process new work without it. Optional downstream systems,
diagnostic services, and recursive dependency trees do not belong there.

Checks share one concurrency bound and one request deadline. Cancellation,
panic, saturation, and timeout yield `unavailable`. A timed-out
cancellation-ignoring check remains globally bounded. Results retain
registration order. The next request retries transient failures.

## Business HTTP contract

The platform retains `net/http` and caller-owned `http.Handler`.
Construction applies the middleware order in `decisions.md`, then creates one
`serverhttp` runtime.

Middleware wrappers MUST preserve supported `ResponseWriter` capabilities
through `Unwrap`, including flushing, hijacking, pushing where available, and
`io.ReaderFrom` where implemented. Streaming, SSE, WebSockets, uploads,
downloads, and long RPC calls remain application contracts and MUST select
compatible write and shutdown timeouts explicitly.

Panic recovery writes a generic 500 only before response commitment. It cannot
retract committed bytes. Panic values do not appear in external responses.
Business responses receive `X-Content-Type-Options: nosniff` by default.
Route-specific cache, content, and TLS policies remain application-owned.

## Correlation contract

Canonical headers are:

```text
X-Correlation-ID
X-Request-ID
X-Causation-ID
```

Extraction validates but never grants trust. Without an accepting trust
callback, inbound values become typed external metadata only and the platform
starts a new correlation workflow.

Every ingress creates a new request ID. A trusted inbound request ID becomes
causation. Child hops preserve correlation, create a new request ID, and use
the parent request ID as causation.

Malformed or conflicting metadata is replaced by default and rejected only by
explicit policy. Correlation values are never metric labels. Idempotency keys
and W3C trace context remain distinct.

Queue, Kafka, RPC, webhook, scheduled, migration, command, batch, retry,
fan-out, and fan-in boundaries use correlation-owned adapters and link
semantics. Application code runs only after valid `correlation.Values` exist.

## Configuration contract

`Invocation.Environment` is an immutable snapshot. Test invocation never
changes process environment.

For local operation, a command MAY explicitly enable bounded `.env` discovery.
For non-local operation, configuration comes from environment or mounted
values. Infisical is a deployment source and is not called by core `service`.

The typed loader returns a validated concrete value before `Build`. Failure
prevents component construction. Error rendering includes the safe field or
source name and excludes values.

The first platform version does not reload configuration.

## Logging and telemetry contract

Logs receive safe service name, role, version, correlation, request,
causation, trace, and bounded request metadata where available. Request
bodies, credentials, tokens, cookies, authorization headers, connection
strings, and raw configuration are excluded by default.

Metric attributes exclude correlation IDs, request IDs, customer values,
unbounded paths, and arbitrary errors. Trace instrumentation MUST avoid a
second server span when a protocol or router already owns it.

Caller-owned logger and telemetry facilities close only through an explicit
adapter component that transfers ownership. Telemetry flush is bounded.

## Resource adapter contract

Construction never hides the concrete client. Adapters connect lifecycle and
readiness only.

- PostgreSQL and Valkey close after HTTP, RPC, worker, and scheduler drain.
- Queue and Kafka consumers stop intake before joining deliveries.
- Producers flush after publishers stop and before transport close.
- Schedulers stop new triggers before joining active executions.
- Migration commands initialize only migration dependencies.
- Cleanup after partial startup is safe and repeated shutdown is defined.

## Error disclosure contract

Typed errors preserve causes and identify a safe component and phase. Error
strings do not contain secret values. External HTTP responses use fixed safe
messages. CLI rendering occurs once and maps through the exit table in
`decisions.md`.

## Security invariants

The platform rejects or bounds hostile headers, correlation values, request
bodies, compressed bodies, proxy metadata, trace baggage, health checks,
signals, shutdown waits, and debug exposure.

Unsafe proxy trust, legacy health aliases, detailed probes, wildcard
management binding, decompression, and production debug behavior require
explicit opt-in. Profiling and configuration diagnostics are absent from the
base management server.
