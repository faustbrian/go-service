# HTTP runtime

`serverhttp` owns one caller-created listener and one `http.Server`. `New`
starts no goroutines. `Run` owns and joins the serving goroutine and may be
called once. This makes address selection, socket activation, test listeners,
and listener security explicit application decisions.

```go
listener, err := net.Listen("tcp", "127.0.0.1:8080")
if err != nil {
    return err
}
server, err := serverhttp.New(
    listener,
    routes,
    serverhttp.WithBodyLimit(2<<20),
    serverhttp.WithShutdownTimeout(20*time.Second),
)
if err != nil {
    listener.Close()
    return err
}
if err := runtime.Go("http", server.Run); err != nil {
    return err
}
```

## Defaults

| Setting | Default |
| --- | ---: |
| Read headers | 5 seconds |
| Read request | 30 seconds |
| Write response | 30 seconds |
| Idle connection | 2 minutes |
| Graceful shutdown | 30 seconds |
| Request body | 1 MiB |
| Request headers | 1 MiB |

Read, header, write, and idle timeout options accept an explicit zero to use
the standard library's disabled-timeout behavior. Shutdown must remain bounded,
so `WithShutdownTimeout` requires a positive value.

## Middleware order

The constructor installs recovery, request IDs, and body limiting outside user
middleware. User middleware retains listed order; the first item is outermost.
`Chain` provides the same ordering for independent composition without a
server.

Inbound request IDs are untrusted by default. Trusted IDs must be non-empty
HTTP tokens within the configured length; invalid values are replaced. Panic
responses contain no panic value or prepared headers if the response was not
committed. Once a handler commits bytes, HTTP cannot replace them; recovery
contains the panic but preserves the already-committed response.

The recovery writer implements `Unwrap`, so Go's `http.ResponseController`
continues to discover supported optional operations on the original writer.

## Shutdown

Context cancellation starts graceful shutdown with the configured bound. If
the bound expires, `Run` force-closes connections and returns a typed
`RunError`. Handlers must honor request cancellation; Go cannot forcibly stop a
handler goroutine that ignores cancellation. Hijacked connections are outside
`http.Server.Shutdown` and remain application-owned, matching `net/http`.

When callers have not configured `http.Server.BaseContext`, `Run` installs its
own context so request handlers observe the same cancellation cause. An
explicit caller `BaseContext` is preserved and remains caller-owned.
