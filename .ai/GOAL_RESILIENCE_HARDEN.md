# Goal: Harden Service Resilience Integration

## Non-Negotiable Quality Gate

The module MUST maintain exactly 100% statement coverage and exactly 100% of
viable mutants killed by meaningful tests. Tests MUST prove behavior rather
than merely execute lines or preserve implementation structure.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

## Required Proof

- Model startup, ready, overload, drain, shutdown, and failure with every
  stateful policy lifecycle.
- Race middleware admission, outbound attempts, policy snapshots, readiness,
  SIGTERM, repeated shutdown, and termination deadlines.
- Prove dependency failure never creates a liveness restart loop.
- Prove drain stops new work, unblocks queued callers, and reports unfinished
  uncooperative work honestly.
- Simulate replica scaling, mixed revisions, cold policy state, backend outage,
  and HPA feedback with bounded fleet amplification.
- Verify no implicit policy stack, algorithm duplication, global registry, or
  unbounded metric identity enters `service`.

Meaningful exact 100% statement coverage, exactly 100% viable mutation kills,
race, leak, fault, lifecycle, benchmark, API compatibility, docs, security, and
supply-chain gates MUST pass.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174
