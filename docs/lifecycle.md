# Lifecycle and ownership

## State model

```text
new --Start--> starting --success--> ready --Drain--> draining
                   |                    |                 |
                   | failure            `--Shutdown------|
                   v                                      v
                stopping ----------------------------> stopped
```

`new` is safe but not ready. `starting` admits no readiness traffic. `ready`
means every configured component started successfully. `draining` rejects new
readiness traffic while existing work is allowed to finish. `stopped` is
terminal and shutdown results are repeatable.

## Startup

Components start in registration order. A component becomes owned by the
runtime only after its start operation succeeds. If any start fails or panics,
the runtime cancels its service context and stops every successfully started
component in reverse order. The returned typed startup error identifies the
failed component and retains both the start failure and rollback failures.
Cancellation observed after a successful hook prevents every later component
from starting; the just-started component is included in rollback.

Component acquisition receives a context with a 30-second operation deadline
by default. `Config.StartupTimeout` overrides that deadline; zero selects the
default and a negative value is invalid. The service lifetime context remains
active after successful startup.

Rollback uses the configured bound. If a stop hook ignores cancellation,
`Start` returns a rollback timeout and the service remains `stopping`; one owned
cleanup coordinator retains the hook and a later `Shutdown` can join it.

Concurrent startup attempts do not execute a component twice. Operations that
are invalid for the current state return a typed state error.

## Draining and shutdown

Drain is an idempotent readiness transition. It invokes each successfully
started component's optional `CloseAdmission` once in reverse startup order.
Admission closure runs before service-initiated shutdown cancellation, must
return promptly, and must not wait for active work. A parent context may already
be canceled. Its failures are returned by `Drain` and retained in the final
shutdown result. Startup rollback also closes admission before component
cleanup.

Shutdown implies drain, cancels the service context with an observable cause,
joins accepted work, and stops owned components in reverse startup order. Use
the context-aware `Stop` operation to drain active permits or shut down policy
observers after accepted work joins. The caller supplies the shutdown context
and therefore owns its deadline. Concurrent shutdown callers observe the same
admission closure and terminal result, subject to their own waiting contexts.

In the cohesive runtime, business HTTP is supervised work. Cancellation closes
its listener and drains in-flight handlers alongside workers before dependency
components close. The management listener remains available until that cleanup
finishes.

Shutdown never silently converts an unbounded caller context into a bounded
one. Higher-level helpers and the HTTP runtime expose explicit timeout options
where they own the policy.

Only one cleanup coordinator runs. A shutdown caller may abandon its wait when
its context expires while cleanup remains owned and joinable by later callers.

## Supervised work

Supervised goroutines receive the service context. Returning an error before
cancellation or a non-cancellation error afterward cancels the service with a
typed cause. A result matching the canceled context's `Err` or cancellation
cause is normal completion; this permits context-aware runners to return their
usual cancellation result during graceful shutdown. Shutdown joins all
supervised work within the caller's bound. A goroutine that ignores
cancellation can make shutdown return the caller's context error, but it cannot
be abandoned or reported as joined.

`Config.MaxTasks` caps concurrently active supervised tasks. Zero selects 64;
values above the hard ceiling of 4096 are rejected. A completed task releases
its slot. Saturation returns a typed configuration error without starting an
extra goroutine.

## Signals

Signal handling is opt-in. The signal subscription is owned by the lifecycle
runner that creates it, is stopped during cleanup, and has a deterministic join
path. The first configured signal begins drain and shutdown; repeated signals
do not start overlapping shutdown sequences. A second signal cancels remaining
work immediately and the original finite cleanup deadline still bounds the
join.

`service.Run` owns its `os/signal` subscription and releases it before return.
`service.RunWithSignals` accepts a caller-owned channel and never closes or
unregisters it. Both accept an explicit shutdown timeout; zero selects the
documented 30-second default. The signal is retained as the service context's
typed cancellation cause.
Nil entries in an owned `RunConfig.Signals` slice are rejected before signal
registration. Caller-owned signal channels must be non-nil and are never
closed by the runtime.

`service.Wait` and `service.WaitWithSignals` provide the same behavior for an
already-started runtime. Use them after registering `Service.Go` tasks. This
keeps task registration explicit without causing a second startup attempt.
Both helpers also observe the service-owned context, so a supervised task
failure begins shutdown even when no process signal or parent cancellation is
received.

The cohesive `Execute` path owns its caller-supplied signal channel from command
construction through startup, runtime work, and cleanup for both long-running
and one-shot commands. The first signal cancels the current operation with a
typed `SignalError`; a second cancels remaining cleanup within the original
bound. `SIGINT` and `SIGTERM` map to exits 130 and 143 unless a cleanup failure
or deadline takes precedence.
