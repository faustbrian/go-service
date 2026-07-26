# FAQ

## Is this an application framework?

No. It coordinates runtime ownership and accepts ordinary functions,
`context.Context`, `http.Handler`, `slog.Logger`, and `net.Listener` values.

## Does it load configuration or secrets?

No. Use `config` or any caller-owned loader before dependent components.

## Does it initialize OpenTelemetry or logging?

No. Providers, handlers, exporters, processors, collectors, and their shutdown
remain in the application composition root.

## Can I use another HTTP router or an RPC framework?

Yes. `serverhttp` accepts any `http.Handler`; other blocking servers can be
components or supervised tasks.

## Why are component and check names required?

Stable names make failures, detailed diagnostics, tests, and ownership records
deterministic. Names must not contain secrets or high-cardinality values.

## Why can a cancellation-ignoring check remain running?

Go cannot safely terminate an arbitrary goroutine. `healthhttp` bounds such
checks globally with its semaphore and rejects later work instead of creating
unbounded goroutines. Checks are still required to honor their context.

## Does shutdown close hijacked HTTP connections?

No. This matches `http.Server.Shutdown`. WebSockets and other hijacked
connections are application-owned and need an explicit drain and join path.
