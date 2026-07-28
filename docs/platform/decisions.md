# Platform decisions

This record freezes the Phase 1 product and API decisions for the cohesive
`service` platform.

The key words "MUST", "MUST NOT", "REQUIRED", "SHALL", "SHALL NOT",
"SHOULD", "SHOULD NOT", "RECOMMENDED", "NOT RECOMMENDED", "MAY", and
"OPTIONAL" in this document are to be interpreted as described in BCP 14
[RFC2119] [RFC8174] when, and only when, they appear in all capitals, as
shown here.

[RFC2119]: https://www.rfc-editor.org/rfc/rfc2119
[RFC8174]: https://www.rfc-editor.org/rfc/rfc8174

## D-001: product and import path

The product name, module name, root package name, and primary import path are
`service`, `service`, `service`, and
`github.com/faustbrian/golib/pkg/service`.

The lifecycle implementation MUST move from `service/service` into the root
package before release. The documentation-only `goservice` package MUST be
removed. No `platform`, `bootstrap`, or second lifecycle package will be
introduced.

## D-002: public construction API

The cohesive entry points are:

```go
func Main(definition Definition) int
func Execute(
    ctx context.Context,
    definition Definition,
    invocation Invocation,
) int
func CommandFor[C any](spec CommandSpec[C]) Command
```

`Main` supplies real process arguments, environment, streams, and signal
handling, and returns an exit code without calling `os.Exit`. `Execute` is the
deterministic in-process entry point and MUST NOT read or mutate `os.Args`,
process environment, global loggers, or global telemetry providers.

The public construction types are:

```go
type Definition struct {
    Identity         Identity
    Commands         Commands
    Logger           *slog.Logger
    Correlation      *correlation.Factory
    TracePropagation serverhttp.Middleware
    Management       Management
}

type Commands struct {
    Serve    Command
    Worker   Command
    Schedule Command
    Migrate  Command
    Custom   []Command
}

type CommandSpec[C any] struct {
    Name        string
    Summary     string
    Kind        CommandKind
    Load        func(context.Context, Invocation) (C, error)
    Build       func(context.Context, BuildContext, C) (Plan, error)
}

type Plan struct {
    Components []Component
    Tasks      []Task
    HTTP       *HTTP
    Readiness  []ReadinessCheck
    Management bool
}
```

`Command` is an immutable opaque registration produced by `CommandFor`.
Generic configuration is erased only inside that immutable command closure.
Application callbacks always receive their concrete `C`; no callback receives
`any`, `map[string]any`, a mutable registry, or a service locator.

`BuildContext` contains only immutable process identity, selected role,
caller-owned logger, and the correlation factory. Optional infrastructure and
business dependencies MUST be ordinary typed values captured by the
application callback or returned in `Plan`.

The supporting types are fixed as:

```go
type Invocation struct {
    Args        []string
    Environment []string
    Stdout      io.Writer
    Stderr      io.Writer
    Signals     <-chan os.Signal
}

type BuildContext struct {
    Identity    ProcessIdentity
    Logger      *slog.Logger
    Correlation *correlation.Factory
}

type Task struct {
    Name string
    Run  func(context.Context) error
}

type HTTP struct {
    Address                  string
    Listener                 net.Listener
    Handler                  http.Handler
    Options                  []serverhttp.Option
    TrustCorrelation         func(*http.Request) bool
    RejectInvalidCorrelation bool
}

type ReadinessCheck struct {
    Name string
    Run  func(context.Context) error
}

type Management struct {
    Address                  string
    Listener                 net.Listener
    Details                  bool
    TrustCorrelation         func(*http.Request) bool
    RejectInvalidCorrelation bool
}
```

Exactly one of `HTTP.Address` and `HTTP.Listener` MUST be set. At most one of
`Management.Address` and `Management.Listener` MAY be set. Empty management
address and listener select the secure default. `Plan.Management` is ignored
for long-running commands, which always expose management, and explicitly
opts a one-shot command in.

`CommandKind` has exactly `CommandKindLongRunning` and
`CommandKindOneShot`. `ProcessIdentity` is `Identity` plus the selected role.
Nil output streams are invalid in `Execute`; `Main` supplies process streams.
Nil signals mean the invocation follows only its parent context. Nil logger or
correlation factory leaves logging disabled or selects the correlation
package's default factory.

`TracePropagation`, when non-nil, is installed on both business and management
HTTP after correlation. The telemetry adapter constructs it from the
caller-owned propagator; core `service` does not interpret W3C state.

## D-003: command model

The standard command names are exactly `serve`, `worker`, `schedule`, and
`migrate`. Custom names MUST be lowercase kebab case and MUST NOT collide with
standard names, `help`, or `version`.

`CommandKindLongRunning` starts a management server.
`CommandKindOneShot` does not. A one-shot command MAY opt in through its
`Management` plan option. Mixed-role work is represented by one explicitly
registered long-running command whose plan names every owned task; unrelated
roles are never combined automatically.

Parsing, help, version output, usage errors, and deterministic invocation MUST
compose the owned `cli` module. `service` MUST NOT implement a second command
tree.

Configuration loading occurs after a command is selected and before its build
callback runs. Therefore a role initializes only configuration and
dependencies it declares. Load and build callbacks receive the same 30-second
construction deadline as component acquisition; runtime tasks receive the
independent process lifetime context.

## D-004: identity

`Identity` contains:

- `Name`;
- `Version`;
- `Commit`;
- `BuildTime`;
- `GoVersion`;
- `Environment`; and
- `Instance`.

The selected command supplies the process role. `Name` is REQUIRED. Empty
optional build values render as `"unknown"` and do not fail startup.
`Version`, `Commit`, and `BuildTime` MUST be syntactically validated when
present. Environment and instance values MUST NOT become metric labels.

Identity is added to logs, telemetry resources, safe diagnostics, outbound
user agents, and health metadata only where the wire contract permits it.

## D-005: lifecycle and plans

Components start sequentially in declaration order and stop in reverse
successful order. The first platform contract does not infer parallel startup.
Future parallel groups require an explicit dependency model and a separate
decision.

Context-aware component acquisition has a 30-second default operation deadline
through `Config.StartupTimeout`. Zero selects the default and a negative value
is invalid. The service lifetime context remains independent after startup.

Tasks start only after required components and the management listener are
owned. Every task has a name, a run function, cancellation through the service
context, and a join path. A task returning unexpectedly is a runtime failure
and terminates the process.

Startup failure rolls back every successfully started component. Readiness is
withdrawn before task intake and business HTTP drain. Business HTTP and tasks
drain before infrastructure components close. The management server stops
last.

## D-006: HTTP ownership

`HTTP` accepts exactly one of a caller-provided listener or an address to bind,
plus a caller-owned `http.Handler` and explicit `serverhttp` options.
Ownership of a provided listener transfers only after successful plan
validation. Construction failure before transfer leaves it caller-owned.

The default business HTTP limits remain:

| Limit | Default |
| --- | ---: |
| read-header timeout | 5 seconds |
| read timeout | 30 seconds |
| write timeout | 30 seconds |
| idle timeout | 2 minutes |
| shutdown timeout | 30 seconds |
| request body | 1 MiB |
| request headers | 1 MiB |

Zero and negative semantics remain those documented by `serverhttp`; the
cohesive path MUST NOT silently disable a bound.

## D-007: management server

Long-running roles receive one dedicated management server. Its default
address is `127.0.0.1:8081`. Kubernetes examples explicitly use
`0.0.0.0:8081`.

| Management limit | Default |
| --- | ---: |
| read-header timeout | 2 seconds |
| read timeout | 5 seconds |
| write timeout | 5 seconds |
| idle timeout | 30 seconds |
| shutdown timeout | 5 seconds |
| request headers | 16 KiB |
| concurrent connections | 256 |

The platform rejects a management address or listener that collides with a
platform-owned business listener. A caller-provided management listener
retains ownership until plan validation succeeds and then transfers
explicitly.

The management router exposes only `/livez`, `/startupz`, and `/readyz`.
Business middleware cannot replace, shadow, authenticate, compress, or
rewrite them.

Successful probe requests are not logged at informational level by default.
The platform emits bounded logs and metrics only for startup, readiness, and
drain transitions plus probe failures. Profiling, configuration, dependency
addresses, and detailed diagnostics are absent.

## D-008: health

`/livez` reports execution of the management runtime and never checks external
dependencies. `/startupz` becomes successful only after required startup
completes. `/readyz` requires startup, non-draining state, and successful
declared readiness checks.

Readiness checks default to a one-second per-probe deadline, four-way
concurrency, and at most 64 checks. Transient failure affects the current
request; the next request evaluates recovery. Details are disabled by default.

`healthhttp` will depend on a package-local state interface, not the root
package. Root lifecycle state will satisfy that interface.

## D-009: correlation and tracing

The `correlation` module exclusively owns correlation, request, causation,
validation, trust, generation, context, carrier, and propagation semantics.
The cohesive path uses its HTTP adapter for both business and management
servers.

The default is replace-invalid and distrust inbound metadata. A trust callback
is the only way to preserve an inbound correlation ID and convert the inbound
request ID to causation. Every local ingress gets a new request ID.

W3C trace extraction is caller-owned optional middleware supplied through the
telemetry adapter. It runs after correlation and before application
middleware. The core does not import OpenTelemetry or set global providers.

## D-010: middleware order

The default outermost-to-innermost order is:

1. panic recovery;
2. correlation extraction and local request generation;
3. W3C trace extraction when configured;
4. trusted proxy/client address resolution when configured;
5. request body limit;
6. decompression when configured;
7. authentication when configured;
8. authorization when configured;
9. rate limiting when configured;
10. request logging;
11. metrics;
12. response security headers; and
13. compression when configured.

Recovery, correlation, body limits, and safe response headers are mandatory.
Trace extraction, trusted proxies, decompression, logging, metrics, and
compression are recommended typed options. Authentication, authorization, and
rate limiting are application-selected composition.

Circuit breakers remain outbound-client behavior.

## D-011: configuration

The command-specific generic `Load` callback is the typed configuration
boundary. The `configservice` adapter supplies bounded local `.env` and
environment orchestration without moving decoding or validation into
`service`.

Source precedence is explicit defaults, optional local `.env`, process
environment, then caller-provided override sources. `.env` discovery is
allowed only for an explicitly local environment. Validation completes before
`Build`.

Decoded documents reject unknown keys. Environment variables outside the
application prefix are ignored; unknown variables inside that prefix are
rejected when the selected `config` source can enumerate them. A source that
cannot enumerate keys states that limitation and MUST NOT claim strict
unknown-key validation.

The first platform release does not reload configuration. Dynamic refresh
requires a separate optional config integration implementing the complete
snapshot, validation, publication, backpressure, rollback, and resource
replacement contract.

## D-012: logging and telemetry

The core accepts `*slog.Logger`. Nil keeps logging disabled and remains nil in
`BuildContext`; construction callbacks must test for its presence before use.
The platform does not construct or close a logger. This preserves caller
ownership and prevents the disabled path from retaining logging initialization
or handler code. Redaction and Better Stack delivery remain logging-package
composition.

Telemetry integrates through `telemetryservice`. Provider registration,
exporters, sampling, propagation, flush, and shutdown are caller-owned and
explicit. Initialization failure policy is either required or best-effort as
selected in that adapter. Shutdown is bounded by the component timeout.

## D-013: errors and exit codes

Typed errors classify definition, usage, configuration, construction, startup,
rollback, runtime, readiness, drain, shutdown timeout, and cleanup failures.
They preserve causes with `errors.Is` and `errors.As` and never render secret
values.

The stable exit map is:

| Exit | Meaning |
| ---: | --- |
| 0 | success, help, or version |
| 1 | finite application command failure |
| 2 | usage or unknown command |
| 70 | invalid definition, construction, runtime, drain, or cleanup failure |
| 75 | transient component startup failure |
| 78 | configuration load or validation failure |
| 124 | shutdown deadline exceeded |
| 130 | interrupted by `SIGINT` |
| 143 | terminated by `SIGTERM` |

An error is rendered once at the process boundary. HTTP responses never use
the process error renderer.

Exit precedence is definition, usage, and configuration before construction;
then startup; then runtime. Shutdown deadline 124 or cleanup 70 overrides a
signal exit. Otherwise the initiating signal maps to 130 or 143. A successful
graceful parent-context cancellation returns 0.

## D-014: release boundary

The module remains pre-release until every platform blocker passes. The first
published version is exactly `pkg/service/v1.0.0`. Phase completion does not
authorize tagging or publication.
