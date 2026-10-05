# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt Tenancy v2.0.0 through its major-version module path so trusted HTTP
  tenant scope reaches authorization and RPC through one context contract.

- Adopt Config v2.0.0 through its major-version module path while preserving
  the reference service's defaults and canonical configuration loader. Select
  Service v1.1.0 to satisfy Config's public dependency contract.

- Allow caller-owned client transports and release fixture connection pools
  before shutdown so unused speculative connections cannot race the existing
  five-second lifecycle assertion.

- Require Go 1.27.0 as the module language and minimum supported toolchain.
- Adopt Authentication v1.2.0, Config v1.1.0's canonical service loader,
  Correlation v1.1.0, and Telemetry v1.2.0 while retaining the server-side
  net/http instrumentation contract.
- Verify the HTTP composition against Validation v1.1.0.

### Added

- maintained public-API HTTP reference service with lifecycle, configuration,
  routing, JSON-RPC, signed capability, HTTP signature, authentication,
  authorization, tenancy, correlation, validation, telemetry, and audit proof
