#!/usr/bin/env bash
set -euo pipefail

module_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

count_source_lines() {
	local path="$1"

	awk '
		BEGIN { in_block = 0; count = 0 }
		{
			line = $0
			trimmed = line
			sub(/^[[:space:]]+/, "", trimmed)

			if (in_block) {
				if (index(trimmed, "*/") == 0) {
					next
				}
				sub(/^.*\*\//, "", trimmed)
				in_block = 0
				sub(/^[[:space:]]+/, "", trimmed)
			}

			while (index(trimmed, "/*") > 0) {
				prefix = substr(trimmed, 1, index(trimmed, "/*") - 1)
				suffix = substr(trimmed, index(trimmed, "/*") + 2)
				if (index(suffix, "*/") == 0) {
					trimmed = prefix
					in_block = 1
					break
				}
				suffix = substr(suffix, index(suffix, "*/") + 2)
				trimmed = prefix suffix
			}

			sub(/^[[:space:]]+/, "", trimmed)
			sub(/[[:space:]]+$/, "", trimmed)
			if (trimmed != "" && substr(trimmed, 1, 2) != "//") {
				count++
			}
		}
		END { print count }
	' "${path}"
}

check_budget() {
	local service_name="$1"
	local file_name="$2"
	local maximum="$3"
	local count

	count="$(count_source_lines "${module_root}/${file_name}")"
	if (( count > maximum )); then
		printf '%s retained %d generic lines; maximum is %d\n' \
			"${service_name}" "${count}" "${maximum}" >&2
		return 1
	fi
	printf '%s retained %d generic lines; maximum is %d\n' \
		"${service_name}" "${count}" "${maximum}"
}

check_budget "Track" "track.go" 500
check_budget "Postal" "postal.go" 125
check_budget "Location" "location.go" 650
