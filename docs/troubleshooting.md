# Troubleshooting

## Startup returns `StartupError`

Use `errors.As` to inspect the failed component and `errors.Is` for the original
cause. The same aggregate may contain rollback failures. Confirm components are
registered in dependency order and do not put dependent work before its
configuration or provider hook.

## Shutdown returns a context error

The caller's bound expired. Identify components or supervised tasks that did
not honor cancellation. Do not increase the timeout before proving which owner
is slow and whether the orchestrator grace period is long enough.

## Readiness remains unavailable

Confirm lifecycle state is `ready`, then enable details only on a protected
endpoint to identify the unavailable check name. Saturation after a timeout
usually means a check ignored cancellation and still occupies a bounded slot.

## HTTP `Run` returns `RunError`

Graceful shutdown failed and a forced close was attempted. Inspect retained
causes with `errors.Is` and `errors.As`. Check for long handlers and application-
owned hijacked connections.

## A request ID is replaced

Inbound trust is off by default. When enabled, IDs must be non-empty HTTP tokens
within the configured maximum length. Whitespace, separators, control bytes,
and header-injection content are rejected.

## `make check` cannot find tools

Install Go, standard POSIX shell tools, and `golangci-lint` v2.12.2. Actionlint
and `govulncheck` are invoked through pinned `go run` commands. CI installs the
same linter version before calling `make check`.
