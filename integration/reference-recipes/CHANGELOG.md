# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Changed

- Adopt Correlation v1.1.0 for the public lifecycle recipes.

### Added

- Explicit ordering assertions that acknowledgement follows successful
  application processing and intake withdrawal precedes admitted-work drain.
- executable public-API recipes for a minimal HTTP service lifecycle and a
  bounded ingester-to-processor handoff with acknowledgement and drain
