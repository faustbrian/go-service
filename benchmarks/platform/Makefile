GO ?= go
BENCH_TIME ?= 250ms
BENCH_COUNT ?= 10
OUTPUT ?= platform-benchmarks.txt
INPUT ?= $(OUTPUT)
PROCESS_OUTPUT ?= artifacts/platform-process
PROCESS_SAMPLES ?= 5
PROCESS_REQUESTS ?= 100000
PROCESS_PROBE_REQUESTS ?= 20000
PROCESS_CONCURRENCY ?= 16

.PHONY: analyze benchmark capture environment process process-smoke test

test:
	$(GO) test ./...

benchmark:
	GIN_MODE=release $(GO) test . -run '^$$' \
		-bench '^Benchmark(Equivalent(PostalSearch|TrackIngestion|TrackJSONRPC|LocationLookup|PipelineConstruction)|WorkerDispatchAndSupervision)$$' \
		-benchmem -benchtime='$(BENCH_TIME)' -count='$(BENCH_COUNT)'

capture:
	GIN_MODE=release $(GO) test . -run '^$$' \
		-bench '^Benchmark(Equivalent(PostalSearch|TrackIngestion|TrackJSONRPC|LocationLookup|PipelineConstruction)|WorkerDispatchAndSupervision)$$' \
		-benchmem -benchtime='$(BENCH_TIME)' -count='$(BENCH_COUNT)' \
		> '$(OUTPUT)'

analyze:
	$(GO) tool benchstat '$(INPUT)'

environment:
	@$(GO) version
	@$(GO) env GOOS GOARCH
	@uname -a
	@sysctl -n machdep.cpu.brand_string 2>/dev/null || true

process:
	$(GO) run ./cmd/measure \
		-output '$(PROCESS_OUTPUT)' \
		-samples '$(PROCESS_SAMPLES)' \
		-requests '$(PROCESS_REQUESTS)' \
		-probe-requests '$(PROCESS_PROBE_REQUESTS)' \
		-concurrency '$(PROCESS_CONCURRENCY)'

process-smoke:
	$(GO) run ./cmd/measure \
		-output '$(PROCESS_OUTPUT)' \
		-samples 1 \
		-requests 100 \
		-probe-requests 100 \
		-concurrency 4 \
		-candidates low-level-service,cohesive-service \
		-states disabled \
		-enforce=false
