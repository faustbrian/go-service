# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Added

- Add a task-owned process-death and PostgreSQL/Valkey container-replacement
  recovery campaign with durable replay and queue reclamation checks.
- Maintained PostgreSQL and Valkey durability composition fixture, including
  transactional rollback isolation and unacknowledged-task recovery after
  consumer restart.
