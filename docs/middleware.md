# Middleware composition cookbook

`serverhttp.Chain` preserves plain `http.Handler` and makes ordering visible.
The first middleware is outermost:

```go
handler, err := serverhttp.Chain(
    routes,
    accessLog,
    authentication,
    authorization,
    trustedPriority,
    principalRateLimit,
    bulkhead,
    adaptiveThrottle,
)
```

For `authentication` and `authorization`, authenticate before authorizing
and keep route policy in the application. Neither package is imported or
initialized by `service`. Request ID and recovery middleware installed by
`serverhttp.New` remain outside user middleware, so authentication logs can use
the correlation ID and both authentication and authorization panics are
contained.

Rate limiting, bulkheading, and adaptive throttling are admission policy, not
authentication. A rejection says that otherwise eligible work is not admitted;
it does not establish who the caller is or what the caller may do. Derive
principal or account keys only after authentication. IP or connection limits
may run before authentication, but must use a separately named policy.

Trusted priority must come from authenticated application context or from a
proxy boundary that strips client-supplied priority metadata. Never accept a
client header as priority merely because it is present. Priority changes
admission probability or queueing only; it does not bypass authentication,
authorization, rate accounting, or resource bounds.

Keep retry, hedge, dependency circuit breakers, adaptive concurrency, and
outbound rate policies inside dependency or HTTP-client pipelines. Placing
them around the inbound handler can retry side effects, multiply accepted
traffic, train dependency state from local rejections, and hide the physical
attempt count. See [resilience composition](resilience.md).

Queue, scheduler, RPC, and ingester adapters should preserve the same rule:
transport identity extraction precedes authentication, authentication precedes
authorization, and domain handlers receive explicit caller-owned values. Do
not put business policy in lifecycle hooks or health checks.

The executable `ExampleChain_authenticationAndAuthorization` in the
`serverhttp` package demonstrates the same adapter boundary with a principal
stored in request context. Replace those two plain middleware functions with
the `http.Handler` adapters supplied by `authentication` and
`authorization`; ordering and ownership do not change.
