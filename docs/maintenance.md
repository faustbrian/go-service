# Maintenance mode

Maintenance mode is an optional platform facility. Configure one
`MaintenanceStore` to add the `down`, `up`, and `status` commands, withdraw
readiness, and install admission control in front of business HTTP. Liveness
remains successful so an orchestrator does not restart a deliberately paused
process.

```go
store, err := service.NewFileMaintenanceStore("/run/example/maintenance.json")
if err != nil {
    return err
}
definition.Maintenance = service.Maintenance{
    Store:            store,
    RefreshInterval:  time.Second,
    OperationTimeout: time.Second,
}
```

The file adapter publishes an at-most-8-KiB JSON snapshot with mode `0600` and
an atomic same-directory rename. It is intended for one host. Multiple
replicas should use `NewSharedMaintenanceStore` with application-owned database
or cache operations that provide coherent reads and atomic publication.

## Commands

```text
SERVICE down [--retry DURATION] [--refresh DURATION]
             [--secret TOKEN | --with-secret] [--redirect /PATH]
SERVICE status
SERVICE up
```

`down` publishes the complete immutable state. `up` clears it. `status` emits
JSON and reports only whether a bypass is configured; it never prints the
token. `--with-secret` is the only operation that generates and writes a token
to standard output. Store failures are classified as temporary failures.

Durations are bounded to seven days. A bypass token is 8 to 128 alphanumeric
or dash characters. Redirects must be absolute paths, may not begin with `//`,
and may not contain control characters.

## Admission behavior

While enabled, business requests receive `503 Service Unavailable`,
`Cache-Control: no-store`, and `X-Content-Type-Options: nosniff`. Optional
`Retry-After` and `Refresh` values come from the published state. A configured
redirect returns `302`; its target remains admitted to prevent a loop.

`Maintenance.Response` may provide already-constructed headers and a body. The
platform still forces status 503 and suppresses the body for `HEAD`. The
handler must not perform unbounded work or depend on facilities being updated.

A `GET` or `HEAD` request to `/<secret>` receives an HTTP-only, secure,
same-site bypass cookie and redirects to `/`. The cookie contains a
domain-separated digest, not the token. The token and cookie must remain
excluded from logs, metrics, traces, errors, and diagnostics. Applications own
authorization and distribution of bypass tokens.

The runtime refreshes the snapshot on a bounded interval. An initial load
failure prevents startup. A later load failure retains the last valid snapshot
and emits a bounded failed maintenance event. Disabling maintenance restores
readiness after the next successful refresh.

## Deployment boundary

The cohesive process constructs business HTTP from the selected application
plan. It therefore cannot serve an in-process response when dependencies or
the application binary are unavailable during construction or replacement.
Use ingress or reverse-proxy maintenance for dependency-breaking, binary
replacement, and zero-process deployment windows. The in-process facility is
for a healthy running binary that must stop admitting business work.
