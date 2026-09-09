# Service composition recipes

This non-production module contains executable recipes that compose released
Golib packages exclusively through their public APIs. It is a clean consumer:
its module file pins published versions and contains no workspace or `replace`
directive.

## Minimal HTTP lifecycle

`TestMinimalHTTPRecipeStartsServesReadinessAndShutsDown` constructs caller-owned
loopback listeners, a `service.Service`, `healthhttp` readiness probes, and two
`serverhttp.Server` values. The service supervises both HTTP runtimes, serves a
real business request, withdraws readiness during drain, and joins both servers
during bounded shutdown.

## Ingester-to-processor lifecycle

`TestIngesterProcessorRecipeHandsOffAcknowledgesDrainsAndShutsDown` uses the
target-oriented queue service adapter with a bounded in-memory transport. The
ingester publishes one correlated delivery, the processor handles and
acknowledges it, drain rejects later intake without canceling accepted work,
and shutdown joins the processor before releasing its transport.

Run both recipes from this directory:

```sh
GOWORK=off go test ./... -count=1
```

The loopback and in-memory boundaries prove package composition and lifecycle
ordering. They do not establish broker durability, deployed networking,
container, load, soak, or production evidence.
