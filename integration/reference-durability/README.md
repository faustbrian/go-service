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
