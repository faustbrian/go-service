#!/bin/sh
set -eu

for file in README.md LICENSE SECURITY.md CONTRIBUTING.md \
    CODE_OF_CONDUCT.md CHANGELOG.md THIRD_PARTY_NOTICES.md \
    docs/architecture.md docs/lifecycle.md docs/http.md docs/health.md \
    docs/integration.md docs/configuration.md docs/middleware.md \
    docs/testing.md docs/adoption.md docs/release.md docs/api.md \
    docs/kubernetes.md docs/migration.md docs/faq.md \
    docs/troubleshooting.md docs/compatibility.md docs/security.md \
    docs/performance.md docs/operations.md docs/hardening.md \
    docs/evidence.md; do
    test -s "$file" || {
        echo "required documentation is missing: $file" >&2
        exit 1
    }
done

go run ./scripts/check-api-docs.go .
go test ./...
go build ./examples/...
