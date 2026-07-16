#!/bin/sh
set -eu

for required in go dirname; do
    command -v "$required" >/dev/null 2>&1 || {
        echo "required safety tool is missing: $required" >&2
        exit 1
    }
done

root=$(dirname "$(go env GOMOD)")
script_dir=$(CDPATH= cd -- "$(dirname "$0")" && pwd)
go run "$script_dir/check-go-safety.go" "$root"
