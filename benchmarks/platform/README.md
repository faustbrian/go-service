# Service platform comparison benchmarks

This non-releasable benchmark module compares equivalent request behavior
across:

- plain `net/http`;
- low-level `service` composition;
- cohesive `service` composition;
- Chi `v5.3.1`;
- Gin `v1.12.0`;
- Echo `v4.15.4`; and
- Fiber `v3.4.0` on fasthttp, reported separately because its runtime contract
  is not `net/http`.

The module exists only to produce auditable pre-release evidence. It is not a
public service API, framework adapter, or recommendation engine.

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
make test
make environment
make capture OUTPUT=platform-benchmarks.txt BENCH_COUNT=10 BENCH_TIME=250ms
make analyze INPUT=platform-benchmarks.txt
make process-smoke PROCESS_OUTPUT=/tmp/service-platform-smoke
make process PROCESS_OUTPUT=artifacts/platform-process
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
source revision, the complete repository gate-input digest, tool versions,
OS, architecture, and raw `oha` artifacts. `report.json` is atomically
checkpointed after every sample.
Rerunning with the same output directory resumes an input-identical completed
sample prefix after verifying the environment, configuration, candidate
binaries, and every checksummed raw artifact. A different gate-input digest or
other mismatched input fails instead of relabeling stale evidence.

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

The process harness does not measure allocations; the in-process benchmarks
own that evidence. Worker dispatch and supervision compare low-level and
cohesive long-running construction around one shared, correlation-aware
fixture. Release evidence still requires repeated quiet-host results for that
comparison.
