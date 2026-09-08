# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt `go-retry` v1.1.0 strict policy construction and execution while
  classifying only the explicit read-only GET operation as known, and pin
  `go-rate-limit` v1.1.0.

### Added

- maintained external-dependency reference composition for HTTP, resilience,
  webhooks, filesystems, and authenticated secret envelopes
