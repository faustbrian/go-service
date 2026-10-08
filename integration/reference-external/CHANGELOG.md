# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt published Webhook v3 for signed delivery and verification, keeping
  explicit endpoint policy, signer ownership and bounded transport behavior.
- Adopt published HTTP Client v2 through its major-version module path while
  preserving bounded requests, response-body ownership and client cleanup in
  the internal external-dependency reference.
- Adopt published Retry v2.1.0 and Hedge v1.1.0 while retaining standalone
  retry and hedge policies without enabling shared Resilience budgets.
- Adopt published Secret Envelope v2 for configuration, context and keyring
  composition. Provider failures retain package/context categories rather
  than provider-specific causes; update fixture imports together.
- Adopt the published `go-webhook/v2` module for signed callback delivery and
  verification in the external-dependency reference.
- Adopt Rate Limit v2 through its major-version import path while preserving
  bounded process-local admission and refusal behavior.
- Require Go 1.27.0 as the module language and minimum supported toolchain.
- Adopt `go-retry` v1.1.0 strict policy construction and execution while
  classifying only the explicit read-only GET operation as known, and pin
  `go-rate-limit` v1.1.0.

### Added

- maintained external-dependency reference composition for HTTP, resilience,
  webhooks, filesystems, and authenticated secret envelopes
