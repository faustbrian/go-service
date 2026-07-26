# Operations guide

Use separate startup, readiness, and liveness routes. Alert on sustained
readiness loss and shutdown failures; do not page on one transient dependency
check without application context.

Record lifecycle state, component name, cancellation cause class, shutdown
duration, and retained error types in application-owned observability. Do not
record raw configuration, secrets, panic values, health error text, or
unbounded request IDs. `service` does not define metric names or initialize
telemetry providers. The application owns trace propagators and bounded metric
labels; do not use raw component names, check names, or request IDs as
high-cardinality telemetry dimensions.

Set the orchestrator grace period above all owned shutdown bounds. Components
stop sequentially in reverse order, so their worst-case budgets compose. HTTP
shutdown has its own bound and force-close fallback. Supervised tasks must
return after service cancellation.

For incidents, capture:

- exact binary version and Go version;
- lifecycle state and first cancellation cause type;
- failed component/check names without secret values;
- shutdown deadline and orchestrator grace period;
- race, goroutine, connection, and request profiles where policy permits;
- the exact `make check` or focused reproduction command.
