#!/usr/bin/env bash
set -euo pipefail

root="$(git rev-parse --show-toplevel)"
module="github.com/faustbrian/golib/pkg/service"
version="v0.0.0"
temporary_root="${TMPDIR:-/tmp}"
consumer="$(mktemp -d "${temporary_root%/}/service-consumer.XXXXXX")"
proxy="$(mktemp -d "${temporary_root%/}/service-proxy.XXXXXX")"
modcache="$(mktemp -d "${temporary_root%/}/service-modcache.XXXXXX")"

cleanup() {
	chmod -R u+w "${modcache}" 2>/dev/null || true
	rm -rf -- "${consumer}" "${proxy}" "${modcache}"
}
trap cleanup EXIT HUP INT TERM

"${root}/scripts/build-local-proxy.sh" "${proxy}" "${version}" pkg/service

cd "${consumer}"
GOWORK=off go mod init example.com/service-consumer >/dev/null
GOWORK=off go mod edit -go=1.26.5

export GOMODCACHE="${modcache}"
export GOPROXY="file://${proxy},${GOLIB_UPSTREAM_GOPROXY:-https://proxy.golang.org,direct}"
export GONOSUMDB="github.com/faustbrian/golib/*"
export GOWORK=off

go get "${module}@${version}"

if ! go mod edit -json | jq -e '.Replace == null' >/dev/null; then
	printf 'clean consumer must not use replace directives\n' >&2
	exit 1
fi

cp "${root}/pkg/service/scripts/testdata/clean-consumer/consumer_test.go" .

go test -mod=readonly ./... -count=1
