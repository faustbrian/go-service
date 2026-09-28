# Changelog

All notable changes to this compatibility module are documented here.

## Unreleased

### Changed

- Verify Scheduler and Telemetry v2 through their major-version module paths
  while preserving the lifecycle and duplicate-registration contracts.
- Require Go 1.27.0 as the module language and minimum supported toolchain.
- Verify Authentication v1.2.0, Config v1.1.0, and Correlation v1.1.0 through
  their immutable public module identities.
- Resolve gRPC v1.83.2 to avoid the xDS server denial-of-service issue in
  GHSA-2v4p-qf9q-27wj.
