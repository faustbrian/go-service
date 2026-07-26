# Goal: Confirm The HTTP Runtime Boundary And Compare Service Stacks

## Objective

Confirm `net/http` as the service runtime contract and gather evidence before
considering any incompatible fasthttp/Fiber support.

## Runtime Boundary

- Keep `net/http` as the supported public HTTP transport.
- Do not duplicate router, middleware, cancellation, streaming, HTTP-version,
  or lifecycle implementations for fasthttp without measured production
  evidence that transport is the actual bottleneck.
- Treat Fiber/fasthttp as a comparative architecture track, not a supported
  adapter, until a separately approved goal changes this boundary.

## Comparative Work

- Compare plain `net/http`, the owned stack, Chi, Gin, and Echo with matched
  lifecycle, routes, middleware, JSON work, probes, timeouts, errors, and
  graceful shutdown.
- Measure startup, idle RSS, binary size, request latency, throughput,
  allocations, peak memory, saturation, cancellation, and shutdown.
- Report Fiber/fasthttp separately with incompatible context, streaming,
  HTTP-version, adapter, and middleware semantics disclosed.
- Use realistic RPC, API, ingestion, and health workloads in addition to trivial
  handlers.

## Completion Criteria

- The transport recommendation is based on reproducible service-level evidence.
- Different architectures are not hidden in one ranking.
- Any future fasthttp support requires a new explicit compatibility and
  maintenance commitment.

