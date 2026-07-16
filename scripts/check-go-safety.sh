#!/bin/sh
set -eu

root=$(go env GOMOD | xargs dirname)

if rg -n --glob '*.go' --glob '!**/*_test.go' \
    '(^|[[:space:]])"unsafe"|//go:linkname|import "C"' "$root"; then
    echo "GO-SAFETY-1 violation" >&2
    exit 1
fi
