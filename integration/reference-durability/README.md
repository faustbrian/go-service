# Durability reference service

This maintained non-production module exercises Golib's PostgreSQL and Valkey
durability stack through public APIs. It is assurance infrastructure, not a
deployable product or an application dependency.

The executable scenario is intentionally bounded to PostgreSQL migrations,
rollback isolation and atomic commit of business, idempotency-completion, and
outbox state, Valkey Streams publication, consumer restart with
unacknowledged-task reclamation, acknowledgement, and command replay. Kafka,
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
