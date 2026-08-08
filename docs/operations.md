# Operations guide

Use separate startup, readiness, and liveness routes. Alert on sustained
readiness loss and shutdown failures; do not page on one transient dependency
check without application context.

Use `Definition.Observer` to map bounded lifecycle, component, task, probe,
maintenance, and request events into application-owned telemetry. The platform
enriches its caller-owned logger with process identity and the configured
correlation disclosure policy. It does not initialize providers or define
metric names. See [runtime observability](observability.md).

Do not record raw configuration, secrets, panic values, health error text, or
unbounded identifiers. Environment, instance, correlation, request, and
causation values are resource or diagnostic attributes, not metric labels.

Set the orchestrator grace period above all owned shutdown bounds. Components
stop sequentially in reverse order, so their worst-case budgets compose. HTTP
shutdown has its own bound and force-close fallback. Supervised tasks must
return after service cancellation.

For planned maintenance, use the optional [maintenance facility](maintenance.md)
to reject business traffic and withdraw readiness while preserving liveness.
Use ingress maintenance when the application cannot remain constructed.

For incidents, capture:

- exact binary version and Go version;
- lifecycle state and first cancellation cause type;
- failed component/check names without secret values;
- shutdown deadline and orchestrator grace period;
- race, goroutine, connection, and request profiles where policy permits;
- the exact `make check` or focused reproduction command.
