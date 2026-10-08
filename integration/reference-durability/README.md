# Durability reference service

This maintained non-production module exercises Golib's PostgreSQL and Valkey
durability stack through public APIs. It is assurance infrastructure, not a
deployable product or an application dependency.

Normal staging and fresh-process recovery select the published Outbox v2.0.0
envelope, PostgreSQL writer and relay together with its queue adapter v2.0.0.
The queue supplier remains at the harness's existing v1.1.2 selection. These
nominal contracts compose with the existing Idempotency v2 adapter without
changing transaction or lifecycle ownership; no Service root release is needed.

PostgreSQL v2 configuration uses an explicit fixture-owned pgx resolver,
including any native environment and TLS-file acquisition. Startup ping remains
explicitly enabled so the scenario still fails before work on unavailable
database dependencies; pool shutdown remains owned by the scenario.

The executable scenario is intentionally bounded to PostgreSQL migrations,
rollback isolation and atomic commit of business, idempotency-completion, and
outbox state, Valkey Streams publication, consumer restart with
unacknowledged-task reclamation, application handling, and command replay. The
recovery consumer is owned by the public queue/service lifecycle: drain first
withdraws intake while an admitted handler remains active, acknowledgement is
observably absent until application processing succeeds, and shutdown releases
the concrete worker exactly once after the admitted work drains. Kafka,
OpenSearch, provider failover, managed-service behavior, load, soak, and
production readiness remain outside this module's evidence boundary.

`check-recovery.sh` adds a destructive but task-owned local recovery campaign.
It terminates a prepared application process with `SIGKILL`, kills and replaces
both dependency containers while retaining their dedicated durable volumes,
proves PostgreSQL and Valkey outages fail closed, and then verifies exact
idempotency replay, business and outbox state, queue reclamation, and
acknowledgement from a fresh process. A second Valkey crash and replacement
proves the consumer group retains zero pending or lagging work. This does not
claim managed failover, network partition, ambiguous PostgreSQL commit,
backup/restore, or cross-region recovery.

`check-version-matrix.sh` runs the same public durability composition against
digest-pinned PostgreSQL 14 through 18 and Valkey 9.1.0. Every backend pair,
network, image pull, Go cache, and module cache is task-owned and removed after
the campaign. The matrix proves supported-version composition; it does not
claim managed-service behavior, upgrade-in-place, failover, or production
capacity.
