#!/bin/sh
set -eu

fuzz_time=${1:-5s}

for required in go dirname find grep sed sort; do
    command -v "$required" >/dev/null 2>&1 || {
        echo "required fuzz tool is missing: $required" >&2
        exit 1
    }
done

targets=$(find . -type f -name '*_test.go' \
    -exec grep -EnH '^func Fuzz[[:alnum:]_]+' {} + \
    | sed -E 's#^([^:]+):[0-9]+:func (Fuzz[[:alnum:]_]+).*#\1 \2#' \
    | sort)

if [ -z "$targets" ]; then
    echo "no fuzz targets found" >&2
    exit 1
fi

echo "$targets" | while read -r file target; do
    package=./$(dirname "$file")
    go test -run '^$' -fuzz "^${target}$" -fuzztime "$fuzz_time" "$package"
done
