# Service platform comparison benchmarks

This non-releasable benchmark module compares equivalent request behavior
across:

- plain `net/http`;
- low-level `service` composition;
- cohesive `service` composition;
- Chi `v5.3.2`;
- Gin `v1.12.0`;
- Echo `v4.16.0`; and
- Fiber `v3.5.0` on fasthttp, reported separately because its runtime contract
  is not `net/http`.

The module exists only to produce auditable pre-release evidence. It is not a
public service API or framework adapter. It consumes released Service `/v3`
`v3.0.0` through the public dependency graph. The
[transport comparison](../../docs/transport-comparison.md) provides the scoped
result, recommendation, native-runtime differences and retrievable observations.

Commands use `GOWORK=off` so the repository workspace cannot substitute its
local root for the pinned public Service v3.0.0 dependency.

## Frozen request contracts

Every candidate executes the same deterministic reference workloads:

- Postal JSON-RPC search at `POST /postal/search`;
- Track ingestion at `POST /track/ingest`;
- Track JSON-RPC ingestion at `POST /track/rpc`, with one correlation child hop
  per event; and
- Location projection at `POST /location/lookup`.

Every workload retains:

- bounded JSON decoding and deterministic bounded results;
- a 1 KiB request-body limit;
- panic containment with a generic response;
- correlation-owned workflow and request identifiers;
- replacement of correlation metadata from an untrusted immediate peer; and
- optional logging and tracing states that do not change the response.

`TestCandidatesPreserveEquivalentHTTPBehavior` and
`TestCandidatesPreserveReferenceWorkloadBehavior` prove the response and
safety contracts before timing. Fiber reproduces the observable contract
through its native fasthttp runtime and is never included in a `net/http`
ranking.

## Running

```sh
GOWORK=off make test
GOWORK=off make environment
GOWORK=off make capture OUTPUT=platform-benchmarks.txt BENCH_COUNT=10 BENCH_TIME=250ms
GOWORK=off make analyze INPUT=platform-benchmarks.txt
GOWORK=off make process-smoke PROCESS_OUTPUT=/tmp/service-platform-smoke
GOWORK=off make process PROCESS_OUTPUT=artifacts/platform-process
```

`BenchmarkEquivalentPipelineConstruction` measures the prepared request
pipeline, not process startup or high-level command compilation. The four
`BenchmarkEquivalent*` workload cases stop their timers while constructing the
pipeline, then report request bytes and allocations.
`BenchmarkWorkerDispatchAndSupervision` compares the same long-running worker
fixture after low-level and cohesive construction. Each dispatch creates the
same correlation child hop, and the executable equivalence test proves both
supervisors drain and join the worker. The cohesive request results are the
steady-state pipelines produced after high-level construction; full HTTP
construction is measured by the process harness. The Makefile sets
`GIN_MODE=release` for benchmark capture so Gin does not perform debug-route
printing as construction work. Retain the raw file, environment, source
revision, dependency versions, tool versions, sample count, and `benchstat`
output with every published result.

## Process methodology

`make process` builds one stripped binary per candidate and middleware state so
binary-size results do not inherit unrelated framework dependencies or
runtime-disabled optional facilities. Every candidate exposes the same
business and management listeners, all four reference workloads, canonical
probes, correlation identifiers, body limit, panic response, optional logging
and tracing states, and `SIGTERM` exit contract. Fiber uses native fasthttp
listeners and remains a separately disclosed incompatible runtime.

The process runner performs one unrecorded warmup launch for every candidate
before timing, then records five independently started samples. Candidate
direction alternates for each sample so sustained background load is
distributed across both sides of relative comparisons. A relative budget fails
only when its recorded median crosses the frozen ratio and an exact one-sided
paired sign test reaches 95% confidence. Frozen absolute latency, throughput,
resource, and lifecycle budgets apply only on the pinned Darwin reference
environment. Other environments retain success and configured-drain
requirements plus every low-level-to-cohesive relative budget. It uses `oha`
with 100,000 requests for each business workload, 20,000 readiness requests,
and concurrency 16. Each sample records startup to successful
`/startupz`, idle RSS, per-workload
p50/p95/p99, throughput and success rate, probe latency, and graceful
shutdown. The report also records stripped binary size, SHA-256 checksums,
original execution revision and dirty-source qualification, latest revalidation revision, the measurement source/lock/build-input digest, tool versions, OS, architecture, effective
`GOMAXPROCS`, performance-affecting Go runtime environment, and raw `oha`
artifacts. `report.json` is atomically checkpointed after every sample.
Rerunning with the same output directory resumes an input-identical completed
sample prefix after verifying the complete recorded environment, configuration,
candidate binaries, and every checksummed raw artifact. A scheduler, garbage
collector, memory-limit, runtime-debug, gate-input, or other input mismatch
fails instead of relabeling stale evidence. Frozen Darwin absolute budgets
apply only with the reference host's default Go runtime environment.
An unrelated commit or history-only rewrite does not invalidate checkpoints
whose complete gate input and rebuilt candidate binary digests remain equal;
the report retains the original execution revision and records the revision at
which those inputs were revalidated.

Every compatible `net/http` candidate also runs a separately started
configured-drain sample. The handler flushes its response header, remains
in-flight for deterministic bounded work after request cancellation, and must
join within the declared 50 ms shutdown deadline plus the frozen 100 ms
allowance. Fiber does not report this value because fasthttp has a different
response and shutdown contract.

The runner enforces the environment-applicable frozen process budgets in
`docs/platform/performance-budgets.md`. Framework results are comparison
evidence; Fiber is never ranked as a `net/http` implementation. `make
process-smoke` proves harness behavior with reduced samples and does not
constitute performance evidence.

The current process harness also records sampled peak RSS during business,
probe and saturation load at a nominal 20 ms interval. Sampler work is joined
on every path, and each `ps` read has a one-second deadline. This is sampled
resident memory, not an OS high-water mark or Go heap allocation. Every sample
retains a fixed Track ingestion sweep at concurrency 1, 16, 64 and 256 with
20,000 requests at each point. A diagnostic plateau is the first median point
with no more than 10% throughput gain and at least 50% higher p95 latency than
the previous point; it is not an open-loop capacity or causal bottleneck claim.
The configuration exposes bounded sweep controls and retains every point,
including HTTP and connection failure rates.

Cancellation runs independently of drain through two bounded, per-process
observation slots. Explicit client cancellation and a 150 ms client deadline
close the connection; the server observes its native request context through a
separate status endpoint. The deadline is not encoded in HTTP or injected into
the server context. Results retain trigger-to-observation latency separately
from handler wait. Fiber's default native-context result can remain unobserved
and ends through a separate 200 ms fixture cleanup timer; an unobserved result
is not zero-latency success. Do not carry pooled Fiber contexts beyond handler
lifetime.

The v4 checkpoint schema includes these measurements, their raw checksums and
sweep configuration. Restore compares additional metric values with raw
evidence and rejects missing or changed inputs. Source hashes include fixture
and measurement Go files, `benchmark_test.go`, module locks and Makefile;
assertion-only tests and prose do not invalidate unchanged captures. Process
binaries use `-buildvcs=false`; source identity is retained in the report so
commit metadata alone does not change behavior-identical binary digests.

The process harness does not measure allocations; the in-process benchmarks
own that evidence. Worker dispatch and supervision compare low-level and
cohesive long-running construction around one shared, correlation-aware
fixture. Release evidence uses repeated results from the accepted sustained-
load host and retains their recorded variability.
