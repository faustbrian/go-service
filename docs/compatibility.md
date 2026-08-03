# Compatibility

The unreleased module currently declares Go 1.26.5 as its language,
standard-library, and toolchain floor. The repository's sole owned CI workflow
uses the exact root `.go-version` on Ubuntu 24.04. Unix-only signal defaults
and subprocess tests use build constraints; non-Unix platforms default to
`os.Interrupt`, but the current hosted matrix does not independently verify
macOS or Windows.

After a maintainer selects and publishes an initial semantic version, the
exported API and documented response contracts follow semantic versioning.
This verification effort does not select or reserve `v1.0.0`. Incompatible
changes after publication require the appropriate version increment and must
be recorded in the changelog and migration documentation.

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
their dependencies to `service` consumers. The repository catalogs and checks
it as an independent module with the same root Go 1.26.5 toolchain.
