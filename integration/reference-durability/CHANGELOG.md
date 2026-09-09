# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt Idempotency v1.1.0's canonical outbox adapter together with the public
  Migrations and PostgreSQL v1.1.0 lifecycle contracts.
- Make the queue composition explicitly reject acknowledgement before
  application processing completes.
- Join recovered delivery handling and acknowledgement to the service-owned
  intake-withdrawal, admitted-work drain, and exactly-once worker shutdown
  lifecycle.
- Repair all durability launchers so they resolve this repository from any
  working directory and pass their digest-pinned image identities to Docker.
- Align the transitive `golang.org/x/text` dependency with the current owned
  module graph.

### Added

- Add a digest-pinned PostgreSQL 14 through 18 durability composition matrix
  against Valkey 9.1.0 with task-owned backend and cache cleanup.
- Add a task-owned process-death and PostgreSQL/Valkey container-replacement
  recovery campaign with durable replay and queue reclamation checks.
- Maintained PostgreSQL and Valkey durability composition fixture, including
  transactional rollback isolation and unacknowledged-task recovery after
  consumer restart.
