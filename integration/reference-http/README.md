# HTTP reference service

This non-production integration module proves that the recommended Golib HTTP
stack composes through public APIs without a private framework layer. It is an
executable assurance fixture, not a deployable product or a runtime dependency
for application services.

## Covered composition

The service exposes a single `POST /rpc` JSON-RPC method and separate platform-
owned management probes. Its request path composes:

- `service` lifecycle, readiness, shutdown, restart, and correlation ownership;
- typed canonical Config service loading and explicit `router` registration;
- RFC 9421 HTTP signatures and SHA-256 content-digest verification;
- URL-bound signed capabilities with application-level authorization;
- tenant extraction, opaque bearer authentication, and RBAC authorization;
- JSON-RPC parameter decoding and bounded Validation v2 validation;
- OpenTelemetry request instrumentation and fail-closed audit delivery.

`Reference.PrepareRequest` adds the short-lived URL capability. The client
returned by `Reference.Client` then computes the content digest and signs the
complete HTTP request. These are test-facing helpers for exercising the real
public adapters; the server still verifies every boundary independently.

Capability v2 binds the signing key, HTTP verifier, and application use to the
explicit `reference-http` issuer. This reference route deliberately authorizes
reusable grants; it does not configure a bounded-use consumption store.

`Config.ClientTransport` permits an explicitly caller-owned HTTP transport.
Callers must release its idle connections after closing response bodies and
before cancelling the service; signing wrappers do not forward transport
cleanup. A nil transport retains the standard default transport. The tests
use isolated pools for signed, unsigned, and management traffic, including
unused speculative connections, without changing graceful-shutdown bounds.

## Evidence boundary

The tests use loopback listeners and in-memory audit and telemetry adapters.
They prove startup, readiness withdrawal and recovery, authorized traffic,
rejected unsigned and unauthenticated traffic, graceful shutdown, and restart
with fresh listeners. They do not establish container, ECS, managed-service,
durability, load, soak, chaos, deployment, or production readiness evidence.

```sh
go test ./integration/reference-http -count=1
```

The repository operational-assurance register remains authoritative for the
overall readiness verdict.
