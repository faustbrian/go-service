# HTTP runtime boundary and service comparison

[Documentation map](index.md) · [Performance guide](performance.md) ·
[Executable methodology](../benchmarks/platform/README.md)

Service supports standard `net/http`, caller-owned `http.Handler`, request
contexts and explicit listener/lifecycle ownership. Fiber/fasthttp remains a
separate comparative architecture, not a supported Service adapter. Future
support requires a separately approved compatibility and maintenance commitment
and production evidence that transport is the actual bottleneck. These
loopback synthetic measurements do not establish a production bottleneck.

## Scope and reproducibility

This is a historical capture. The recorded results measured published
`github.com/faustbrian/go-service/v2` `v2.0.0`, and reproduce only from the
harness source recorded with the report. The benchmark module now consumes
`go-service/v3` `v3.0.0`, which has no new capture; running the steps below
today measures v3 and produces a different source/lock digest.

The captured comparison consumed published `github.com/faustbrian/go-service/v2`
`v2.0.0`, whose immutable source is
`0a09f11b6e2478070673eb1b7eb12d0bf20489ff`. The measurement harness's actual
source/lock/build-input digest and dirty-source qualification are recorded
separately; the baseline Git revision alone does not contain the new harness.
All capture commands use `GOWORK=off`; a workspace-enabled run can substitute
the local root and is not this released public baseline.
Other dependencies are Chi v5.3.2, Gin v1.12.0, Echo v4.16.0, Fiber v3.5.0,
fasthttp v1.73.0 and Correlation v1.1.2. The complete graph is in the report.

Every candidate retains bounded Postal search JSON-RPC, Track API ingestion and
JSON-RPC child-hop fan-out, Location API projection, and health workloads.
Results compare disabled, logging and tracing states independently. Compatible
business listeners match routes, identifiers, JSON work, panic containment,
1 KiB body limits and read/header/write/idle timeouts. Every process also exposes
management probes and signal exit behavior, but management timeout and size
policies are not equivalent. Application data and external I/O are deterministic
fixtures, not deployed service algorithms.

Management-listener settings in the measured candidates are:

| Candidate group | Header / read / write / idle timeout | Configured header / body limit |
| --- | --- | --- |
| Cohesive Service v2 public management | 2 s / 5 s / 5 s / 30 s | 16 KiB headers; body limit disabled (`0`) |
| Low-level Service | 5 s / 30 s / 30 s / 120 s | 1 MiB headers; 1 MiB body limit |
| Plain net/http, Chi, Gin and Echo (`processcore`) | 5 s / 30 s / 30 s / 120 s | 1 MiB headers; no body-limit wrapper |
| Fiber/fasthttp | No separate header timeout / 30 s read / unlimited write / 120 s idle | Native 4 KiB read buffer and 4 MiB parser body limit; fixture middleware rejects bodies above 1 KiB |

The Fiber native defaults are recorded in the pinned
[v3.5.0 configuration](https://github.com/gofiber/fiber/blob/v3.5.0/app.go);
its read buffer is not a standard `MaxHeaderBytes` contract. The captured
management workload uses small, bodyless readiness requests and does not
exercise these differing thresholds. Management slow-header/body, large-header
and large-body behavior is untested by this comparison; matching probe results
do not prove equivalent management-resource policies.

There are five independently started process samples per candidate/state,
100,000 requests per business workload, 20,000 readiness requests and concurrency
16. Candidate order alternates after an unrecorded warmup. Startup is launch to
successful startup probe; shutdown is SIGTERM to process exit. Idle RSS is read
before load. Peak RSS is sampled during business/probe/sweep load at a nominal
20 ms interval with bounded, joined `ps` calls; it is not an OS high-water mark
and can miss short peaks. Sampler overhead is present for every candidate.

The bounded closed-loop saturation diagnostic runs Track ingestion at
concurrency 1, 16, 64 and 256, with 20,000 requests per point. It retains
throughput, p50/p95/p99 and failure rates at every point. The first median point
with at most 10% throughput gain and at least 50% higher p95 than its predecessor
is called an observed plateau. This is neither open-loop capacity nor proof of
a particular bottleneck. No observed plateau means only that this bounded
criterion was not met.

Allocation measurements are repeated in-process benchmarks, not network
allocations or heap high-water marks. Standard HTTP uses recorder dispatch;
Fiber uses native `App.Test`, whose extra test-runtime costs are not a
transport-normalized allocation ranking. Gin route-debug output is disabled.
Stripped process binaries are built separately for every candidate/state,
without VCS metadata in the binary; report source identities and binary hashes
own attribution. Runtime defaults and host environment remain recorded inputs.

The frozen performance budgets are unchanged. Their absolute reference
qualification requires exactly Go 1.27.0 on the recorded Darwin host; a current
Go 1.27.2 capture cannot pass or redefine those absolute budgets. Portable
success/drain requirements and applicable low-level-to-cohesive relative
budgets remain enforced. New peak/sweep/cancellation diagnostics do not invent
replacement release budgets. Historical v1 reports remain historical, not
current-v2 proof, and the deferred ecosystem Benchmark/Performance program is
not a prerequisite or claimed complete.

## Cancellation and incompatible architectures

Cancellation is measured separately from graceful drain. A client explicitly
closes its request or reaches a 150 ms client deadline; a separate status
request reads the handler's native request-context observation. HTTP does not
transmit that deadline into the server context. Trigger-to-observation latency
uses the shared host clock; handler-start-to-completion wait is a separate
metric. The fixture provides two bounded per-process observation slots and
joins requests; it does not retain pooled contexts or accumulate a registry.

Fiber's default `Context()` is not cancelled by client disconnect; an explicitly
supplied context can instead carry application deadlines. Native SSE can use its
own stream context and disconnect handling. Consequently this comparison does
not claim that Fiber cannot stream or that installing an application deadline
makes its native request lifecycle equivalent. See Fiber's
[context guide](https://docs.gofiber.io/guide/go-context/) and
[cancellation/streaming explanation](https://docs.gofiber.io/blog/fiber-v3-cancellation-timeouts-disconnects/).

| Boundary | Standard HTTP track | Fiber/fasthttp track |
| --- | --- | --- |
| Context and lifetime | Immutable request context; disconnect cancels accepted work | Pooled native context; default stored context has no disconnect signal; values must not escape handler lifetime |
| Cancellation measurement | Native request context, with separate disconnect/deadline observations | Same client actions, but an unobserved native context ends through a separate 200 ms cleanup timer; no supported latency is reported |
| Streaming | `ResponseWriter`/`ResponseController`, flushing and standard request cancellation | Native stream writer/SSE APIs and their own error/disconnect handling; adapter conversion does not prove equivalent capabilities |
| HTTP versions | This comparison measures HTTP/1.1 only; Service retains separately tested standard HTTP/2 support | This fixture uses native fasthttp listeners; no HTTP/2 or HTTP/3 equivalence is claimed |
| Middleware and adapters | `http.Handler`, standard wrappers and preserved writer capabilities | Native Fiber middleware and copied bounded JSON bodies; no supported Service adapter or general middleware interchangeability |
| Lifecycle | Equivalent no-work signals and compatible configured drain; framework fixture `processcore` does not force-close arbitrary handlers after shutdown failure | Native shutdown has a separate one-second timeout; the flushed configured-drain contract is not claimed or ranked |
| Business-listener timeouts | Shared 5 s header, 30 s read/write and 120 s idle settings; management settings differ as disclosed above | Native 30 s read and 120 s idle settings; separate header/write semantics are not disguised as equivalent |

Matching JSON responses does not erase these differences. Fiber results belong
in their own architecture section, not a single speed ranking.

## Reproduce the selected workload

Use Go 1.27.2 and `oha` 1.15.0. The reference Darwin/arm64 executable is the
[official release asset](https://github.com/hatoo/oha/releases/download/v1.15.0/oha-macos-arm64),
verified against published SHA-256
`70d7cb7c15ed3d5eb4b7d9a7e76f0a8ee32ba1f18f560acef3b28e8670b89bb0`.
The runner records the executable digest and versions. Use the recorded
architecture-specific equivalent for another platform and report its separate
environment; do not apply the Darwin absolute reference limits there.

From `benchmarks/platform`, retain the report and raw observations before
removing the task-owned caches and binaries:

```sh
set -eu
comparison_dir="$(mktemp -d)"
export GOCACHE="$comparison_dir/cache" GOMODCACHE="$comparison_dir/modules"
export GOTMPDIR="$comparison_dir/tmp" GOWORK=off GOFLAGS=-p=2
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
cleanup() {
  chmod -R u+w -- "$GOMODCACHE"
  rm -r -- "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
  if [ -d "$comparison_dir/process/binaries" ]; then
    rm -r -- "$comparison_dir/process/binaries"
  fi
}
trap cleanup EXIT
GIN_MODE=release make process PROCESS_OUTPUT="$comparison_dir/process" \
  PROCESS_SAMPLES=5 PROCESS_REQUESTS=100000 PROCESS_PROBE_REQUESTS=20000 \
  PROCESS_CONCURRENCY=16 PROCESS_SATURATION_REQUESTS=20000 \
  PROCESS_SATURATION_CONCURRENCY=1,16,64,256
make capture OUTPUT="$comparison_dir/allocations.txt" BENCH_COUNT=10 BENCH_TIME=250ms
make analyze INPUT="$comparison_dir/allocations.txt" > "$comparison_dir/benchstat.txt"
printf 'Retained observations: %s\n' "$comparison_dir"
```

Pin tools, supply task-owned `GOCACHE`, `GOMODCACHE` and `GOTMPDIR`, and keep them
within one bounded session. A full process report requires all 105 completed
samples; a process-smoke run, partial checkpoint or failed budget verdict is
not passing performance qualification.

## Results and retained observations

The complete [capture archive](transport-comparison-data.tar.gz) has SHA-256
`6ad96b4ba0e0bcd2a33371c5595e3630ea87df292e151908a108e7035fa95dbb`. It retains the unchanged original `process/report.json`, all 1,050 raw JSON observations, ten-sample allocation capture, pinned `benchstat` analysis and host setup information.

Execution began 2026-10-14T10:33:49.071878Z on Darwin/arm64, Apple M4 Max, 16 logical CPUs, 128 GiB, AC power, Go 1.27.2 and checksum-verified `oha` 1.15.0. Normal competing daily-work load was accepted; no otherwise-idle host or production capacity is claimed. The measured source/lock/build digest is
`e8528bf6902bc6616d43c7118bb71653c4b900f37f70e6ccefff16a9f3c09872`. The original source revision is the released Service baseline above, with `source_dirty=true`; the new harness was uncommitted when measured. Its actual inputs and candidate binary identities are recorded independently.

All 105 process samples completed. All 525 ordinary business/probe loads at concurrency 16 returned expected HTTP 200 responses without connection errors. All compatible candidates observed both native cancellation triggers in every sample and met the configured-drain observation; Fiber observed neither native trigger and completed through the bounded cleanup timer.

The existing portable normal-load success/drain and disabled owned-stack relative decision rules passed. Go 1.27.0 absolute-reference budgets were inapplicable, not passed or redefined. Passing the five-sample significance rule does not establish tight statistical equivalence: the medians and ranges still show substantial host-load variability.

### Standard HTTP architecture

Values below are separately constructed process observations, not a speed ranking. Startup/shutdown are empirical p95 milliseconds from five launches; memory columns are maximum idle/sampled-load RSS in MiB. Request columns show median Track ingestion RPS and p95 milliseconds. The full report retains all four business workloads and probes with p50/p95/p99, success and throughput.

#### Disabled middleware

| Candidate | Binary MiB | Startup ms | Idle / peak MiB | Track RPS / p95 ms | Shutdown ms | Plateau concurrency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| plain-net-http | 6.18 | 36.59 | 11.72 / 31.67 | 46,720 / 0.86 | 1.70 | not observed |
| low-level-service | 6.30 | 31.74 | 11.98 / 31.28 | 75,014 / 0.41 | 3.79 | not observed |
| cohesive-service | 6.61 | 58.88 | 12.45 / 33.06 | 57,117 / 0.66 | 1.90 | 64 |
| chi | 6.43 | 139.58 | 11.95 / 32.92 | 35,724 / 1.23 | 2.48 | not observed |
| gin | 13.29 | 63.60 | 13.83 / 34.41 | 69,615 / 0.50 | 4.66 | 64 |
| echo | 6.36 | 54.82 | 12.16 / 33.53 | 81,296 / 0.40 | 1.60 | not observed |

#### Logging middleware

| Candidate | Binary MiB | Startup ms | Idle / peak MiB | Track RPS / p95 ms | Shutdown ms | Plateau concurrency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| plain-net-http | 6.18 | 60.37 | 11.84 / 31.02 | 28,787 / 1.57 | 6.91 | 64 |
| low-level-service | 6.31 | 42.81 | 12.34 / 32.98 | 35,018 / 1.41 | 3.32 | not observed |
| cohesive-service | 6.61 | 150.94 | 12.48 / 33.31 | 48,910 / 0.82 | 4.30 | 64 |
| chi | 6.43 | 223.73 | 11.91 / 33.78 | 38,702 / 1.12 | 6.91 | not observed |
| gin | 13.29 | 306.28 | 14.14 / 35.28 | 37,492 / 1.16 | 5.94 | not observed |
| echo | 6.36 | 238.98 | 12.23 / 32.75 | 45,726 / 0.89 | 4.36 | not observed |

#### Tracing middleware

| Candidate | Binary MiB | Startup ms | Idle / peak MiB | Track RPS / p95 ms | Shutdown ms | Plateau concurrency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| plain-net-http | 6.18 | 169.18 | 12.02 / 32.00 | 36,126 / 1.11 | 2.57 | 64 |
| low-level-service | 6.31 | 256.43 | 12.20 / 33.61 | 45,826 / 0.92 | 6.23 | not observed |
| cohesive-service | 6.61 | 179.03 | 12.62 / 31.98 | 43,531 / 0.87 | 7.16 | 64 |
| chi | 6.43 | 131.14 | 11.89 / 34.19 | 42,925 / 0.97 | 4.58 | 64 |
| gin | 13.29 | 221.95 | 14.00 / 34.41 | 66,349 / 0.56 | 7.42 | not observed |
| echo | 6.36 | 161.48 | 11.97 / 33.36 | 47,658 / 0.81 | 2.98 | not observed |

### Fiber/fasthttp architecture

These native-runtime results have the incompatible semantics described above and are not combined with standard HTTP into a ranking. Native disconnect/deadline latency is unsupported in every sample; observed cleanup wait was approximately 200 ms. The native one-second shutdown configuration differs from the compatible configured-drain contract.

| Middleware | Binary MiB | Startup ms | Idle / peak MiB | Track RPS / p95 ms | Shutdown ms | Plateau concurrency |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| disabled | 7.12 | 139.30 | 12.92 / 32.22 | 66,534 / 0.54 | 207.62 | 256 |
| logging | 7.14 | 32.35 | 12.75 / 36.45 | 54,448 / 0.65 | 203.58 | not observed |
| tracing | 7.12 | 31.50 | 12.89 / 36.09 | 76,221 / 0.44 | 203.12 | not observed |

### Allocation observations

Low-level and cohesive prepared pipelines retained identical allocation counts in every sample. This is the prepared-handler composition claim only, not full-network allocation equivalence. All candidates, construction and worker cases remain in the raw capture/benchstat report.

| Workload | Disabled allocations, each owned pipeline | Logging | Tracing |
| --- | ---: | ---: | ---: |
| Postal RPC | 47 | 49 | 49 |
| Track ingestion | 51 | 53 | 53 |
| Track RPC | 52 | 54 | 54 |
| Location API | 49 | 51 | 51 |

### Saturation and cancellation limits

Every concurrency 1, 16 and 64 sweep completed without failures. At concurrency 256, 31 of 105 sweep observations had connection/reset/body-write/cancellation errors; the worst observed failure fraction was 13.175%. Every received HTTP response still had the expected 200 status. These failures are preserved in the raw `errorDistribution` and success rates, not classified as normal-load success. The descriptor soft limit observed at synthesis was 65,536, so an FD-limit cause is not established.

A sweep plateau or high-concurrency connection failure does not identify a transport bottleneck. Host scheduling, server/listener behavior and load-generator/network pressure remain possible contributors; distinguishing them requires controlled targeted telemetry, not replacing this bounded pass with a broader benchmark campaign. Raw sweep RPS is the tool-reported request rate; when failures occur, successful goodput is that rate multiplied by success fraction.

Native standard-HTTP cancellation latency is retained per candidate/state and sample, separately from the approximately 150 ms handler wait for a client deadline. Fiber’s approximately 200 ms bounded wait and `observed=false` explicitly mean no measured cancellation latency, not zero-latency success.

### Recommendation

Keep `net/http` as the supported Service contract. This matched service-level comparison demonstrates successful realistic ordinary workloads, bounded standard request cancellation and graceful drain while preserving caller-owned handlers and standard HTTP capability. The owned construction layer adds no prepared-pipeline allocations in these samples and passes the selected portable comparison rules; its noisy process medians do not establish universal overhead equivalence.

The separate Fiber track supplies comparative numbers but does not preserve default disconnect/deadline semantics or establish that transport limits production. No stable fastest-framework ranking, production throughput guarantee, open-loop service ceiling, HTTP/2 or HTTP/3 performance result, or supported fasthttp adapter follows from this capture. Future support still needs a new explicit compatibility/maintenance goal and production bottleneck evidence.

### Archive mapping and audit

The archive retains original bytes. Report references of the form `<original task directory>/process/raw/<name>#sha256=<digest>` resolve to archive member `process/raw/<name>`; the original temporary prefix is provenance, not a dependency on a retained local directory. Verify that member’s SHA-256 against the embedded digest. The same mapping covers regular loads, each saturation point and additional memory/cancellation observations. Recompute summaries from the five samples; additional observation files and raw `oha` metrics retain the underlying values.

The archive contains no task binaries or Go caches. Rebuild candidate binaries from the recorded measurement inputs for independent reproduction; archive checksums and source/graph identities remain their actual integrity boundaries. The historical ignored `.artifacts` paths in the performance guide are not this result’s only proof.
