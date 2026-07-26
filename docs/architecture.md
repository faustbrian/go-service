# Architecture

## Boundary

`service` owns process-local runtime composition. It does not load
configuration, implement domain policy, initialize observability exporters, or
provide transports other than the optional standard `net/http` runtime.

Applications construct dependencies first and pass ready-to-use values into
small runtime components. This keeps the dependency graph directed from the
application to independently importable packages:

```text
application
  |-- service       lifecycle and ownership
  |-- serverhttp    caller-provided http.Handler
  |-- healthhttp    caller-provided dependency checks
  `-- integration   caller-owned logging and telemetry values
```

No package registers global state or starts background work from `init`.

## Package ownership

`service` owns lifecycle state, component start/stop ordering, process signal
subscription when requested, cancellation causes, and joins for supervised
work.

`serverhttp` owns only the `http.Server`, listeners explicitly transferred to
it, middleware installed by its constructor, and the bounded shutdown attempt.
The caller owns the handler and any resources reachable from it.

`healthhttp` owns check scheduling and response encoding. Check dependencies
and their resources remain caller-owned.

`integration` adapts caller-owned facilities. It never creates or shuts down a
logger handler, telemetry provider, exporter, or collector.

`servicetest` provides deterministic barriers and recorders. Production code
does not import it.

## Configuration

Configuration is loaded and validated before lifecycle startup. Applications
may use `config`, plain flags, environment variables, or any other source.
`service` accepts constructed configuration values but contains no source,
merge, discovery, validation-engine, or secret-refresh implementation.

## Dependency policy

Core packages use the standard library. Optional third-party adapters must be
isolated in separately imported packages so importing one concern cannot pull
in unrelated SDKs.
