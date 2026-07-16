# Hardening evidence and release verdict

## Lifecycle and ownership matrix

| Resource | Owner | Cancel or close path | Join or terminal proof |
| --- | --- | --- | --- |
| component | `service.Service` after successful start | reverse `Stop` | stop returns or caller bound fails |
| supervised task | `service.Service` | service context cause | shutdown waits for task count zero |
| OS signal subscription | `Run` or `Wait` | deferred `signal.Stop` | no helper goroutine is created |
| HTTP listener | `serverhttp.Server` after `New` | `Shutdown`, then `Close` | `Run` receives `Serve` result |
| HTTP request body | `net/http` and handler | server/request cancellation | handler contract |
| dependency check | `healthhttp.Probes` | per-check context | result or bounded semaphore quarantine |
| logger/provider | application | application policy | never owned by this module |

## Threat model

Covered hostile conditions include oversized known and streaming bodies,
header injection through request IDs, invalid option combinations, panic before
and after response commit, signal delivery in a subprocess, concurrent and
abandoned shutdown callers, partial startup rollback, cleanup failure and
panic, parent cancellation, cancellation-ignoring health checks, probe
saturation, and forced HTTP close failure.

Slow headers, reads, writes, and idle connections are bounded by independent
`http.Server` settings. HTTP/2 behavior is delegated to the Go standard library
because this module neither configures an alternate HTTP/2 stack nor replaces
`http.Server`.

## Findings

| ID | Severity | Finding | Disposition |
| --- | --- | --- | --- |
| H-001 | high | concurrent first barrier waiters could double-close | fixed with `sync.Once` and race regression |
| H-002 | high | `Wait` replaced its parent cause with shutdown | fixed with cause-preserving regression |
| H-003 | high | checks above concurrency could be skipped | fixed with bounded queue regression |
| H-004 | high | probe capture buffered before truncation | fixed with write-time bound regression |
| H-005 | high | stop hooks could trap shutdown callers | fixed with owned cleanup coordinator |
| M-001 | medium | statement-free root and examples confused coverage scope | root skipped only when no statements; examples build in docs gate |
| M-002 | medium | HTTP requests lacked the run context by default | fixed with real-listener cause regression |
| L-001 | low | ignored check cancellation can retain a goroutine | bounded globally, documented contract, later probes saturate safely |
| L-002 | low | `net/http` cannot retract committed panic output | panic contained, limitation documented |

## Current evidence

Local evidence on 2026-07-16:

- `make check FUZZ_TIME=1s BENCH_TIME=1x`: passed;
- exact statement coverage: 100.0% for `service`, `serverhttp`, `healthhttp`,
  `integration`, and `servicetest`;
- `go test -race ./...`: passed;
- all five fuzz targets: passed one-second smoke runs;
- all four allocation benchmarks: passed;
- `govulncheck`: no vulnerabilities found;
- examples: all compiled.

## Release verdict

The current tree is a locally verified pre-`v1` release candidate. Publishing
`v1.0.0` remains blocked until the GitHub compatibility, security, and release
workflows pass on the hosted repository and `RELEASE_SIGNING_PUBLIC_KEY` is
configured. No hosted result or signed tag exists in this local workspace, so
this document does not claim release readiness beyond the evidence above.
