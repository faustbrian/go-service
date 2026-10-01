# Security policy

## Supported versions

Security fixes are provided for the latest stable major release. Before the
initial publication, only the latest development revision is supported.

## Reporting a vulnerability

Do not open a public issue. Use GitHub's private vulnerability reporting for
this repository. Include affected versions, impact, a minimal reproduction,
and any known mitigations. Reports are acknowledged within seven days.

## Runtime security boundary

Callers remain responsible for TLS policy, trusted proxy boundaries,
authentication, authorization, secret delivery, and dependency security.
Probe details are disabled by default. External HTTP error responses never
contain panic values or stack traces.

The versioned [threat model](docs/threat-model.md) and
[security guide](docs/security.md) define the package-owned controls and
accepted caller-owned boundaries. Context deadlines do not forcibly terminate
application callbacks, logging handlers, or operating-system file operations.

Production code must satisfy `GO-SAFETY-1`: no `unsafe`, cgo, or
`go:linkname`.
