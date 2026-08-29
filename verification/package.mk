.PHONY: docs clean-consumer kubernetes

docs:
	go run ./scripts/check-api-docs.go .
	go test ./... -run '^Example' -count=1
	go build ./examples/...

clean-consumer:
	./scripts/check-clean-consumer.sh

kubernetes:
	./scripts/check-kubernetes.sh
