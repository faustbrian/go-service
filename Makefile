GO ?= go
FUZZ_TIME ?= 5s
BENCH_TIME ?= 100ms
ACTIONLINT_VERSION ?= v1.7.12

.PHONY: benchmark check clean-consumer coverage docs format format-check fuzz \
	integration-compatibility interoperability kubernetes lint race \
	release-major release-minor release-patch safety test vet vuln workflows

format:
	gofmt -w .

format-check:
	test -z "$$(gofmt -l .)"

vet:
	$(GO) vet ./...

lint:
	golangci-lint run --timeout=5m

test:
	$(GO) test ./...

race:
	$(GO) test -race ./...

coverage:
	./scripts/check-coverage.sh

fuzz:
	./scripts/check-fuzz.sh "$(FUZZ_TIME)"

benchmark:
	$(GO) test ./... -run '^$$' -bench . -benchmem \
		-benchtime="$(BENCH_TIME)"

kubernetes:
	./scripts/check-kubernetes.sh

integration-compatibility:
	$(MAKE) -C ../.. tidy-check MODULES=pkg/service/compatibility
	cd compatibility && $(GO) test -race ./...
	$(MAKE) -C ../.. vulnerability MODULES=pkg/service/compatibility

clean-consumer:
	./scripts/check-clean-consumer.sh

interoperability:
	$(MAKE) integration-compatibility
	$(MAKE) clean-consumer

safety:
	./scripts/check-go-safety.sh

docs:
	./scripts/check-docs.sh

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

workflows:
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) \
		-no-color -shellcheck=

check: format-check vet lint test coverage race safety docs workflows fuzz benchmark vuln interoperability

release-patch:
	@scripts/release.sh patch

release-minor:
	@scripts/release.sh minor

release-major:
	@scripts/release.sh major
