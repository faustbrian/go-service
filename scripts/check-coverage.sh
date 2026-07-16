#!/bin/sh
set -eu

packages=$(go list ./... | awk '!/\/examples\//')

for package in $packages; do
    profile=$(mktemp)
    trap 'rm -f "$profile"' EXIT HUP INT TERM
    go test -coverprofile="$profile" "$package"
    if [ "$(wc -l < "$profile" | tr -d ' ')" = "1" ]; then
        rm -f "$profile"
        trap - EXIT HUP INT TERM
        continue
    fi
    total=$(go tool cover -func="$profile" | awk '/^total:/ {print $3}')
    rm -f "$profile"
    trap - EXIT HUP INT TERM

    if [ "$total" != "100.0%" ]; then
        echo "coverage for $package is $total, want 100.0%" >&2
        exit 1
    fi
done
