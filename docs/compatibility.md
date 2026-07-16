# Compatibility

The development line declares Go 1.25 as its minimum language and standard-
library version. CI tests Go 1.25 and the current stable Go release on Linux,
macOS, and Windows. Unix-only signal defaults and subprocess tests use build
constraints; non-Unix platforms default to `os.Interrupt`.

Before `v1.0.0`, incompatible API changes may occur in minor releases and must
be recorded in the changelog and migration documentation. After `v1`, exported
API and documented response contracts follow semantic versioning.

Stable compatibility surfaces intended for `v1` are:

- lifecycle states, ordering, cancellation, and typed error inspection;
- plain `http.Handler`, `net.Listener`, and `http.Server` integration;
- health response field names and status values;
- standard `context` and `log/slog` behavior;
- independently importable packages with no initialization side effects.

No compatibility promise covers error strings, internal log message wording,
benchmark numbers, goroutine scheduling, or undocumented implementation types.
