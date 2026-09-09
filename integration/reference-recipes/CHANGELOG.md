# Changelog

All notable changes to this integration module are documented here.

## Unreleased

### Added

- Explicit ordering assertions that acknowledgement follows successful
  application processing and intake withdrawal precedes admitted-work drain.
- executable public-API recipes for a minimal HTTP service lifecycle and a
  bounded ingester-to-processor handoff with acknowledgement and drain
