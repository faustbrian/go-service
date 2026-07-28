# Adoption guides

The programs under `examples` are complete composition roots and compile in
CI. They use the cohesive root API and intentionally include only the concerns
needed by each service shape.

## HTTP API

Register `serve` with `service.CommandFor` and return a `service.Plan` whose
`HTTP` field contains the application-owned handler and address. The platform
composes `serverhttp`, correlation, the separate management listener, signals,
and ordered shutdown. See `examples/http-api`.

## RPC server

Mount the caller-owned RPC transport as an `http.Handler` in the `serve` plan.
The runnable `examples/rpc` program serves a real standard-library `net/rpc`
endpoint; replacing it with another protocol does not change the platform
boundary. The runtime has no RPC protocol or discovery dependency.

## Worker

Return caller-owned dependencies as components and each consumer loop as a
named task from the `worker` plan. The loop must honor cancellation and own
acknowledgements or retries according to the queue client contract. See
`examples/worker`.

## Ingester

Register an explicit long-running `ingest` command and return a narrow HTTP or
RPC handler from its plan. Keep parsing, persistence, and queue publication in
application packages. See `examples/ingester`.

## Scheduler

Return the scheduler loop and its caller-owned dependencies from the
long-running `schedule` plan. The platform exposes management probes and stops
new scheduling before dependency cleanup. Schedule and leader-election
semantics remain in the scheduler package. See `examples/scheduled-command`.

## Migration

Return migration dependencies as components and finite migration work as tasks
from the one-shot `migrate` plan. The platform maps errors and runs cleanup
without opening a management listener or initializing unrelated resources. See
`examples/migration`.

## Mixed command binary

Register `serve`, `worker`, `schedule`, and `migrate` in one immutable
`service.Definition`. Each selected command loads and constructs only its own
dependencies. The `examples/mixed-role` program demonstrates the command
surface without combining unrelated roles in one process.

No example requires a service locator, dependency-injection container, global
registry, router, database, queue, or configuration format.
