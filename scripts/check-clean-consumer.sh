#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
module="github.com/faustbrian/go-service"
version="${GOLIB_CLEAN_CONSUMER_VERSION:-v1.0.0}"
upstream_proxy="${GOLIB_UPSTREAM_GOPROXY:-https://proxy.golang.org,direct}"
temporary_root="${TMPDIR:-/tmp}"
consumer="$(mktemp -d "${temporary_root%/}/service-consumer.XXXXXX")"
modcache="$(mktemp -d "${temporary_root%/}/service-modcache.XXXXXX")"
gocache="$(mktemp -d "${temporary_root%/}/service-gocache.XXXXXX")"
gotmpdir="$(mktemp -d "${temporary_root%/}/service-gotmp.XXXXXX")"

cleanup() {
	local path
	for path in "${consumer}" "${modcache}" "${gocache}" "${gotmpdir}"; do
		chmod -R u+w "${path}" 2>/dev/null || true
		find "${path}" -depth -delete 2>/dev/null || true
	done
}
trap cleanup EXIT HUP INT TERM

cd "${consumer}"
export GOCACHE="${gocache}"
export GOMODCACHE="${modcache}"
export GOTMPDIR="${gotmpdir}"
export GOPROXY="${upstream_proxy}"
export GONOSUMDB=""
GOWORK=off go mod init example.com/service-consumer >/dev/null
GOWORK=off go mod edit -go=1.26.6
export GOWORK=off

go get "${module}@${version}"

if ! go mod edit -json | jq -e '.Replace == null' >/dev/null; then
	printf 'clean consumer must not use replace directives\n' >&2
	exit 1
fi

cp "${root}/scripts/testdata/clean-consumer/consumer_test.go" .

go test -mod=readonly ./... -count=1
