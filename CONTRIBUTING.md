# Contributing

Use a conventional branch name and conventional commits. Every implementation
change must update `CHANGELOG.md`, add a regression test that demonstrates the
missing behavior, and preserve meaningful 100% production-package coverage.

Run `make check` before opening a pull request. Run longer fuzz targets with
`make fuzz` when changing parsers, options, middleware, health payloads, or
integration wiring.

Public APIs need complete Go documentation. New interfaces must be justified
by more than one implementation. Dependencies require an explanation of why
the standard library is insufficient and an update to
`THIRD_PARTY_NOTICES.md`.
