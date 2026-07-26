# Configuration and secret delivery

`service` consumes constructed values; it does not read files, environment
variables, remote stores, or secret managers. Load and validate configuration
before components that depend on it. `config` is the recommended optional
loader, but plain flags or any other caller-owned source work equally well.

Two valid patterns are:

1. Load configuration before constructing the service. This is simplest when
   configuration determines the dependency graph.
2. Put an `integration` configuration hook first. This is useful when the
   surrounding process constructs a fixed graph but startup loading can fail.

In the second pattern, a load failure stops startup before later components and
is preserved as a typed `service.StartupError`. The failing hook is not cleaned
up because it never transferred ownership; earlier successful hooks are rolled
back in reverse order. Sensitive `config` sources preserve their underlying
cause for `errors.Is` policy while redacting that cause from rendered error
text. The real-module compatibility gate executes this failure path.

For Kubernetes, prefer platform delivery through environment variables or
mounted files using an Operator, CSI driver, or agent. Applications then load
ordinary sources through `config`. Native secret-manager clients remain
optional application dependencies. Never place secret values in component
names, health check names, request IDs, log attributes, or returned errors
intended for external responses.

Automatic secret refresh is outside `service`. When an application adopts
immutable refreshed snapshots, the application owns publication, consumer
rotation, stale-value policy, and shutdown ordering.

See the executable `integration.ExampleNew` for the startup-hook pattern. It is
run in CI, including the same error-handling path used by any `config`
loader.
