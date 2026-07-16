#!/bin/sh
set -eu

fuzz_time=${1:-5s}

targets=$(rg -n --glob '*_test.go' '^func Fuzz[[:alnum:]_]+' . \
    | sed -E 's#^([^:]+):[0-9]+:func (Fuzz[[:alnum:]_]+).*#\1 \2#')

if [ -z "$targets" ]; then
    echo "no fuzz targets found" >&2
    exit 1
fi

echo "$targets" | while read -r file target; do
    package=./$(dirname "$file")
    go test -run '^$' -fuzz "^${target}$" -fuzztime "$fuzz_time" "$package"
done
