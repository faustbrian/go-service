# Health and readiness

`healthhttp` provides independently mountable liveness, startup, and readiness
handlers. Responses use one stable JSON contract and never include dependency
errors. Detailed mode exposes only configured check names and `ok` or
`unavailable` status values.

```go
probes, err := healthhttp.New(healthhttp.Config{
    Lifecycle: runtime,
    Checks: []healthhttp.Check{{
        Name: "database",
        Run:  database.PingContext,
    }},
    CheckTimeout:   time.Second,
    MaxConcurrency: 4,
})
mux.Handle("/live", probes.Liveness())
mux.Handle("/startup", probes.Startup())
mux.Handle("/ready", probes.Readiness())
```

Liveness reports only that the HTTP process can respond. Startup succeeds in
`ready` and `draining`; it is unavailable before startup, during cleanup, and
after stop. This conservative mapping prevents a failed startup that rolled
back to `stopped` from becoming a false-positive Kubernetes startup result.
Readiness requires `service.StateReady`; drain and shutdown transitions reject
new readiness traffic immediately.

## Dependency bounds

Checks run concurrently by default and retain registration order in detailed
responses. `ModeSequential` forbids overlap. Every check has a context timeout,
the shared semaphore caps scheduled check work across concurrent requests, and
`MaxChecks` caps the registered result set. The concurrency slot is acquired
before package-owned goroutines are created, so queued checks do not amplify a
probe request into one waiting goroutine per registration. Hard construction
limits are 256 concurrent checks and 1024 registered checks.

Checks above the concurrency limit wait for a slot within their own timeout;
they are not discarded merely because earlier checks are active.

A check must honor context cancellation. A cancellation-ignoring check cannot
be killed safely by Go; it remains quarantined in one semaphore slot, so
repeated probes cannot create unbounded check goroutines. Later probes report
that saturated dependency unavailable until it returns.

Panics are contained as unavailable results. Check error and panic values are
never serialized.
