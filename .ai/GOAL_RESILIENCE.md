# Goal: Service Resilience Integration

## Non-Negotiable Quality Gate

The module MUST maintain exactly 100% statement coverage and exactly 100% of
viable mutants killed by meaningful tests. Tests MUST prove behavior rather
than merely execute lines or preserve implementation structure.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

## Objective

Integrate the owned resilience modules into `service` lifecycle and adoption
guidance without implementing their algorithms or creating a hidden default
policy stack.

## Required Integration

- Define lifecycle adapters for shared executors and stateful policies that need
  admission closure, drain, snapshot, or shutdown.
- Extend inbound middleware guidance for rate limiting, bulkheading, adaptive
  throttling, and trusted priority without confusing them with authentication.
- Keep breaker, retry, hedge, adaptive concurrency, and outbound rate policies
  in dependency/HTTP-client pipelines.
- Make composition order, logical-versus-attempt scope, total deadline, and
  shared budget visible in service construction.
- Expose readiness and diagnostics without making dependency degradation or an
  open breaker fail liveness.
- Provide application-owned named policy construction with bounded identity;
  no global registry or automatic route/vendor discovery.

## Kubernetes Contract

Document per-pod versus distributed state, aggregate capacity at min/max
replicas, cold start, mixed revisions, HPA feedback loops, readiness withdrawal,
SIGTERM admission closure, accepted-work drain, termination-grace expiry, and
abrupt loss. Configuration examples MUST derive bounds from downstream capacity
rather than publish arbitrary defaults.

## Verification And Acceptance

Add adoption tests for API/RPC, worker, scheduler, and one-shot roles using
representative policy compositions. Exercise startup rollback, policy
construction failure, concurrent drain, blocked waiters, active attempts,
observer failure, and repeated shutdown.

Meaningful exact 100% statement coverage and exactly 100% viable mutation kills
remain mandatory. Race, leak, Kubernetes lifecycle, docs, benchmark, API,
security, and supply-chain gates MUST pass. `service` remains a composition and
lifecycle owner, never a resilience algorithm framework.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174
