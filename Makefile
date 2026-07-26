GO ?= go
FUZZ_TIME ?= 5s
BENCH_TIME ?= 100ms
ACTIONLINT_VERSION ?= v1.7.12

.PHONY: benchmark check coverage docs format format-check fuzz \
	integration-compatibility lint race release-major release-minor \
	release-patch safety test vet vuln workflows

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

integration-compatibility:
	cd compatibility && $(GO) mod tidy -diff
	cd compatibility && $(GO) test -race ./...
	cd compatibility && $(GO) run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

safety:
	./scripts/check-go-safety.sh

docs:
	./scripts/check-docs.sh

vuln:
	$(GO) run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...

workflows:
	$(GO) run github.com/rhysd/actionlint/cmd/actionlint@$(ACTIONLINT_VERSION) \
		-no-color -shellcheck=

check: format-check vet lint test coverage race safety docs workflows fuzz benchmark vuln

release-patch:
	@scripts/release.sh patch

release-minor:
	@scripts/release.sh minor

release-major:
	@scripts/release.sh major
