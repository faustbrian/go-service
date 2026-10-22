# Adoption guides

[Documentation map](index.md) · [Repository README](../README.md)
[Next: Lifecycle and ownership](lifecycle.md)

The programs under `examples` are complete composition roots and compile in
CI. They use the cohesive root API and intentionally include only the concerns
needed by each service shape.

## Migrating to v3

Replace `github.com/faustbrian/go-service/v2` imports with
`github.com/faustbrian/go-service/v3`, and `github.com/faustbrian/go-cli` imports
with `github.com/faustbrian/go-cli/v2`. `CommandSpec.Options` and
`Invocation.Input()` now use go-cli v2 types, so both modules move together.
v2 is no longer maintained.

Every command now parses through the canonical go-cli grammar. Definitions in
which some command declared options, or that configure a maintenance store,
already used it and see no change. Definitions whose commands declare no
options change as follows:

| Arguments | v2 | v3 |
|---|---|---|
| `SERVICE COMMAND --` | exit 2 | runs the command |
| `SERVICE --` | exit 2 | exit 0, nothing runs |
| `SERVICE COMMAND extra --help` | exit 2 | help, exit 0 |
| `SERVICE -- COMMAND`, `SERVICE -` | unknown option, exit 2 | unknown command, exit 2 |

Other exit codes, `--help`, `--version` and `version` are unchanged.

## Migrating to v2

Published v1 and v2 types are distinct.

`down --with-secret` still generates and stores a bypass credential, but no longer
prints it. Scripts that parsed the command output must instead retrieve it through
a protected caller-owned `MaintenanceStore`, or provide a preselected `--secret`.
Safe confirmation and status output remain available. No new credential transport
is introduced. Standalone sequential probes retain per-check timeouts bounded by
the parent context; the cohesive root's concurrent probes share a request deadline.

## HTTP API

Register `serve` with `service.CommandFor` and return a `service.Plan` whose
`HTTP` field contains the application-owned handler and address. The platform
composes `serverhttp`, correlation, the separate management listener, signals,
and ordered shutdown. See [`examples/http-api`](../examples/http-api/main.go).
Construct inbound rate, bulkhead, and adaptive-throttle policies in this
command's `Build` callback. Inject the named policy set into middleware and add
its lifecycle component to the plan.

## RPC server

Mount the caller-owned RPC transport as an `http.Handler` in the `serve` plan.
The runnable [`examples/rpc`](../examples/rpc/main.go) program serves a real
standard-library `net/rpc` endpoint; replacing it with another protocol does
not change the platform boundary. The runtime has no RPC protocol or discovery dependency.
RPC admission follows the same inbound policy boundary as HTTP and does not
turn admission into authentication.

## Worker

Return caller-owned dependencies as components and each consumer loop as a
named task from the `worker` plan. The loop must honor cancellation and own
acknowledgements or retries according to the queue client contract. See
[`examples/worker`](../examples/worker/main.go).
Place breaker, retry, hedge, adaptive concurrency, and outbound rate policies
at each dependency client call. The worker plan owns their lifecycle; it does
not initialize an inbound stack.

## Ingester

Register an explicit long-running `ingest` command and return a narrow HTTP or
RPC handler from its plan. Keep parsing, persistence, and queue publication in
application packages. See [`examples/ingester`](../examples/ingester/main.go).

For the complete handoff boundary, the non-production
[composition recipes](../integration/reference-recipes/README.md) module
composes a correlated queue producer with a processor lifecycle. Its executable
test proves that drain rejects new ingester intake while accepted work can finish and be acknowledged before
bounded shutdown releases the processor transport.

## Scheduler

Return the scheduler loop and its caller-owned dependencies from the
long-running `schedule` plan. The platform exposes management probes and stops
new scheduling before dependency cleanup. Schedule and leader-election
semantics remain in the scheduler package. See
[`examples/scheduled-command`](../examples/scheduled-command/main.go).
Scheduled calls use the same explicit outbound dependency composition as
workers, with one caller-owned total deadline per logical run.

## Migration

Return migration dependencies as components and finite migration work as tasks
from the one-shot `migrate` plan. The platform maps errors and runs cleanup
without opening a management listener or initializing unrelated resources. See
[`examples/migration`](../examples/migration/main.go).
One-shot commands construct only policies required by their selected
dependencies, use process completion as health, and still close admission and
drain policy observers during cleanup.

## Mixed command binary

Register `serve`, `worker`, `schedule`, and `migrate` in one immutable
`service.Definition`. Each selected command loads and constructs only its own
dependencies. The [`examples/mixed-role`](../examples/mixed-role/main.go)
program demonstrates the command surface without combining unrelated roles in
one process.

No example requires a service locator, dependency-injection container, global
registry, router, database, queue, or configuration format.

The clean-consumer
[composition recipes](../integration/reference-recipes/README.md) module also
provides the smallest real-listener HTTP lifecycle recipe. It starts business
and management servers through public APIs, exercises a request and readiness, withdraws
readiness on drain, and joins both servers on shutdown.
