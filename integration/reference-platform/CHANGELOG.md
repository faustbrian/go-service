# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Run the platform fixture on released Service `/v3` v3.0.0 and build it with
  Go 1.27.3.
- Require Go 1.27.0 as the module language and minimum supported toolchain.
- Resolve the platform fixture through Correlation v1.1.0.

### Added

- Add a constrained native-Linux load campaign with explicit latency,
  throughput, heap, goroutine, descriptor, and error budgets.
- disposable Linux amd64 and arm64 platform reference service and container
  assurance harness
