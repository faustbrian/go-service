# Resilience composition

`service` owns construction and lifecycle placement. The focused resilience
modules own their algorithms. There is no default stack, global registry,
route scan, vendor discovery, or policy selection in `service`.

## Application-owned policy sets

Construct one bounded, named policy set in the selected command's `Build`
callback. Names are reviewed operational identities such as `inventory-db` or
`carrier-api`, never request paths, tenant IDs, vendor input, or credentials.
Use the identity and cardinality bounds enforced by `resilience`, `bulkhead`,
`circuit-breaker`, and `adaptive-throttle`. Store the constructed values in an
application struct and inject that struct into handlers, workers, or scheduler
tasks. Do not publish it through a package global or service locator.
Names that enter service lifecycle observations are additionally capped by
`service.MaxRuntimeIdentityBytes`; overlong component, task, command, service,
and readiness names are rejected before execution.

Construction errors abort `Build` before listeners or tasks start. If a later
component fails during startup, the policy lifecycle component closes
admission and participates in reverse rollback.

## Lifecycle adapters

`integration.Hooks.CloseAdmission` maps synchronous, idempotent admission
closure into `service.Component.CloseAdmission`. The service calls it once in
reverse startup order when drain begins and before service-initiated
cancellation; a parent context may already be canceled.
Use `Hooks.Stop` for context-bounded drain or shutdown after accepted work has
joined:

```go
component, err := integration.New("inventory-db-policies", integration.Hooks{
    CloseAdmission: databaseBulkhead.Close,
    Stop: func(ctx context.Context) error {
        return errors.Join(
            databaseBulkhead.Drain(ctx),
            databaseBreaker.Shutdown(ctx),
        )
    },
})
```

Use the same closure hook for a shared semaphore or process-local rate store
that only exposes `Close`. Immutable executors, retry policies, and policies
without lifecycle work need no component. Snapshot-only policies remain
application-owned values; read their bounded snapshot APIs directly.

Admission closure must return promptly and must not wait for active permits.
Drain and observer shutdown belong in `Stop`, where the caller-provided
shutdown context supplies the bound. Repeated drain and shutdown reuse the
first admission-closure and cleanup result. Closure, drain, shutdown, observer,
and rollback failures remain typed lifecycle causes.

## Explicit execution scope and order

The caller context is the total deadline. Every attempt timeout must be a
shorter child; no retry, hedge, or client timeout may extend or detach from the
total context.

Logical policies wrap attempt policies. Supply `resilience.Executor` policies
outer-to-inner when adapters implement its policy contract:

```text
total deadline
  shared retry and hedge budget scope
    retry or hedge                    logical scope
      circuit breaker                attempt scope
        adaptive concurrency         attempt scope
          outbound rate policy       attempt scope
            client transport         physical attempt
```

If focused executors are composed directly, preserve the same nesting in
ordinary Go calls. Retry and hedge must attach one shared `resilience.Budget`
scope for the logical operation. Local admission denials remain local
rejections; they are not downstream failures and must not train a breaker or
adaptive limiter as dependency overload.

For inbound API or RPC work, apply the caller-owned rate limit, bulkhead, and
adaptive throttle before domain execution. Do not put retry, hedge, dependency
breaker, or outbound adaptive concurrency around the inbound handler. Worker,
scheduler, and one-shot roles usually have no inbound stack; they use the
outbound dependency pipeline at each client call.

## Readiness and diagnostics

Liveness answers only whether the process and management handler can respond.
An open dependency breaker, adaptive throttling, rate rejection, or dependency
degradation must not fail liveness.

Readiness represents the process's ability to accept its role. Withdraw it on
service drain, maintenance, or when a required dependency makes the role
unable to accept useful work. Do not automatically mirror every breaker state
or transient downstream error into readiness: doing so can remove capacity
and amplify an overload. One-shot roles use process completion, not probes.

Export bounded module snapshots and observations through application-owned
diagnostic endpoints or telemetry. Keep policy name, state, admission counts,
active permits, bounded queue depth, and rejection reason low-cardinality.
Never export request identity, tenant identity, raw errors, or policy maps as
metric labels. Observer failure is diagnostic failure; it does not rewrite the
settled work result.

See [Kubernetes operation](kubernetes.md) for replica capacity and termination
semantics and [middleware](middleware.md) for the inbound trust boundary.

The adoption hardening suite exercises dependency success, failure, overload,
queued admission, active uncooperative work, concurrent snapshots and
readiness, repeated shutdown, and deadline expiry across the application-owned
policy composition. Its deterministic fleet model covers scale-out, mixed
revisions, cold policy state, backend outage, and HPA feedback while checking
the max-replica-derived physical-attempt bound.
