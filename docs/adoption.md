# Adoption guides

The programs under `examples` are complete composition roots and compile in
CI. They intentionally use only the concern needed by each service shape.

## HTTP API

Use `serverhttp` with an application-owned `http.Handler` and listener. Mount
`healthhttp` handlers on the existing mux. Start the lifecycle, supervise
`Server.Run`, then use `service.Run` for process signals. See
`examples/http-api`.

## RPC server

Supervise the chosen RPC transport like any other blocking server and put its
graceful close path under explicit lifecycle ownership. The runnable
`examples/rpc` program serves a real standard-library `net/rpc` endpoint over
`serverhttp`; replacing it with another protocol does not change the lifecycle
boundary. The runtime has no RPC protocol or discovery dependency.

## Worker

Start constructed dependencies as components, then use `Service.Go` for each
consumer loop. The loop must select on its context, return after cancellation,
and own acknowledgements or retries according to the queue client contract.
See `examples/worker`.

## Ingester

Use a narrow HTTP handler or caller-selected RPC transport for ingestion and
supervise it like any other blocking server. Keep parsing, persistence, and
queue publication in application packages. See `examples/ingester`.

## Scheduled command

A finite command can run synchronously as its only startup component and then
shut down immediately. This preserves typed startup errors and cleanup without
installing signal handling unnecessarily. See `examples/scheduled-command`.

## Mixed-role service

Start shared dependencies once, then supervise the independently named server,
consumer, processor, and scheduler loops used by the process. Any supervised
failure drains the whole process and retains its cancellation cause. The
runnable `examples/mixed-role` program combines HTTP with all three background
roles; `examples/rpc` shows the equivalent server seam for RPC.

No example requires a service locator, dependency-injection container, global
registry, router, database, queue, or configuration format.
