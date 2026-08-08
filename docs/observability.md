# Runtime observability

`Definition.Logger` and `Definition.Observer` are optional caller-owned
facilities. The platform does not initialize providers, exporters, handlers,
or global telemetry state and does not close either value.

When a logger is supplied, `BuildContext.Logger` and platform logs are enriched
with available service name, version, commit, build time, Go version,
deployment environment, instance, and selected process role. Correlation,
request, and causation attributes follow `CorrelationDisclosure`. The current
context is passed to `slog`, allowing a caller-owned handler to attach trace
and span identity without a core OpenTelemetry dependency.

`RuntimeObserver` receives a bounded `RuntimeEvent` vocabulary for:

- construction and startup;
- component start and stop;
- readiness, drain, and shutdown;
- supervised tasks;
- liveness, startup, and readiness probes;
- maintenance transitions and refresh failures; and
- business HTTP method, status, and duration.

Observers are synchronous, may be called concurrently, must return promptly,
and must not panic. A panic is contained and does not change service behavior.
The platform logs successful
probe state transitions and failures, but does not log every successful probe.
Observers still receive each probe result so a metrics adapter can count it.

`RuntimeEvent.Identity` is suitable for telemetry resource attributes.
Environment and instance identity, correlation values, arbitrary component or
check names, request paths, headers, bodies, raw errors, and customer data must
not become metric labels. An adapter should map only the bounded event kind,
result, method, status class, and reviewed boundary vocabulary to instruments.
