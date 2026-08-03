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

`make check` also runs pinned workflow validation through `actionlint`. The
tool is executed with `go run` at the repository-pinned version, so local and
hosted checks validate the syntax and expression contracts of the root
`.github/workflows/ci.yml`. Root architecture tests use `go list` and Go's
parser to enforce the exact allowed production dependency graph and reject
`init` functions. A package cannot silently acquire an optional SDK, another
runtime concern, or import-time side effect while the complete gate remains
green.

`make integration-compatibility` enters the isolated `compatibility` module and
executes pinned real-module composition under the race detector, followed by a
reachable vulnerability scan. The catalog checks that module independently
with the same root Go 1.26.5 toolchain.

`make kubernetes` is the explicit disposable-cluster lifecycle gate. It is not
part of the routine package check because it requires Docker, downloads a
checksum-pinned kind binary, and creates a temporary Kubernetes cluster. A pass
atomically records input-fingerprinted evidence under
`.artifacts/pkg/service/kubernetes/report.json`; interrupted or failed runs do
not replace the last complete report.

The health concurrency regression runs inside a `testing/synctest` bubble. It
counts scheduled check goroutines at the saturation boundary and the bubble
cannot finish while a package-owned goroutine remains unjoined. Real-listener
tests explicitly close response bodies and verify graceful, forced, active,
and pre-run listener closure.

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
