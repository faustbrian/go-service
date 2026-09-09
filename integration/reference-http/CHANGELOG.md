# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt Authentication v1.2.0, Config v1.1.0's canonical service loader,
  Correlation v1.1.0, and Telemetry v1.2.0 while retaining the server-side
  net/http instrumentation contract.
- Verify the HTTP composition against Validation v1.1.0.

### Added

- maintained public-API HTTP reference service with lifecycle, configuration,
  routing, JSON-RPC, signed capability, HTTP signature, authentication,
  authorization, tenancy, correlation, validation, telemetry, and audit proof
