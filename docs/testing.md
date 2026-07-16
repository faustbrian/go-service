# Deterministic testing

`servicetest` provides synchronization primitives for lifecycle tests without
timing sleeps.

`Barrier` has explicit entered and release edges, supports any number of
concurrent waiters, propagates context cancellation causes, and has a safe zero
value. Use it to stop a component at a precise transition before starting a
concurrent drain or shutdown call.

`NewComponent` creates a normal `service.Component` with optional start and
stop barriers, injected failures, and a concurrent event recorder. `Recorder`
returns immutable snapshots so assertions cannot mutate recorded state.

`Probe` invokes an HTTP handler and returns status, cloned headers, and a
strictly bounded body. The hard 16 MiB capture ceiling prevents a mistaken
test handler from allocating an unbounded retained result. Bytes past the
requested limit are discarded during each write, not buffered and truncated
afterward.

```go
var start servicetest.Barrier
component, _ := servicetest.NewComponent(servicetest.ComponentConfig{
    Name:         "worker",
    StartBarrier: &start,
})
go runtime.Start(ctx)
<-start.Entered()
// Assert the service is starting, then choose the next transition.
start.Release()
```
