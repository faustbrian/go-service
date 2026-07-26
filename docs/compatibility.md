# Compatibility

The `v1` line declares Go 1.25 as its minimum language and standard-library
line. CI resolves the latest security patch in Go 1.25 and the current stable
Go release on Linux, macOS, and Windows instead of accepting a stale hosted
tool cache. Unix-only signal defaults and subprocess tests use build
constraints; non-Unix platforms default to `os.Interrupt`.

Starting with `v1.0.0`, the exported API and documented response contracts
follow semantic versioning. Incompatible changes require a new major version
and must be recorded in the changelog and migration documentation.

Stable compatibility surfaces are:

- lifecycle states, ordering, cancellation, and typed error inspection;
- plain `http.Handler`, `net.Listener`, and `http.Server` integration;
- health response field names and status values;
- standard `context` and `log/slog` behavior;
- independently importable packages with no initialization side effects.

No compatibility promise covers error strings, internal log message wording,
benchmark numbers, goroutine scheduling, or undocumented implementation types.

The optional `compatibility` module is not part of the importable core API. It
pins real sibling module revisions to detect integration drift without adding
their dependencies to `service` consumers. Its hosted gate uses current
stable Go because authentication and authorization may advance their minimum
toolchain independently of the core module's Go 1.25 support.
