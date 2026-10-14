.PHONY: docs candidate-consumer clean-consumer kubernetes

docs:
	go run ./scripts/check-api-docs.go .
	go test ./... -run '^Example' -count=1
	go build ./examples/...

candidate-consumer:
	GOWORK=off go test -mod=readonly ./scripts/testdata/clean-consumer -count=1 -timeout=2m

clean-consumer:
	./scripts/check-clean-consumer.sh

kubernetes:
	./scripts/check-kubernetes.sh
