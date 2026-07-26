# Middleware composition cookbook

`serverhttp.Chain` preserves plain `http.Handler` and makes ordering visible.
The first middleware is outermost:

```go
handler, err := serverhttp.Chain(
    routes,
    accessLog,
    authentication,
    authorization,
)
```

For `authentication` and `authorization`, authenticate before authorizing
and keep route policy in the application. Neither package is imported or
initialized by `service`. Request ID and recovery middleware installed by
`serverhttp.New` remain outside user middleware, so authentication logs can use
the correlation ID and both authentication and authorization panics are
contained.

Queue, scheduler, RPC, and ingester adapters should preserve the same rule:
transport identity extraction precedes authentication, authentication precedes
authorization, and domain handlers receive explicit caller-owned values. Do
not put business policy in lifecycle hooks or health checks.

The executable `ExampleChain_authenticationAndAuthorization` in the
`serverhttp` package demonstrates the same adapter boundary with a principal
stored in request context. Replace those two plain middleware functions with
the `http.Handler` adapters supplied by `authentication` and
`authorization`; ordering and ownership do not change.
