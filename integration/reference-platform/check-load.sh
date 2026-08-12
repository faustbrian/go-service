#!/bin/sh
set -eu

root=$(CDPATH='' cd -- "$(dirname -- "$0")/../../../.." && pwd)
run_directory=$(mktemp -d "${TMPDIR:-/tmp}/golib-reference-load.XXXXXX")
builder="golib-reference-load-$$"
image="golib-reference-load:$$"
container="golib-reference-load-$$"
fixture_pid=""

cleanup() {
	if [ -n "$container" ]; then
		docker rm --force "$container" >/dev/null 2>&1 || true
	fi
	docker image rm --force "$image" >/dev/null 2>&1 || true
	docker buildx rm --force "$builder" >/dev/null 2>&1 || true
	if [ -n "$fixture_pid" ]; then
		kill "$fixture_pid" >/dev/null 2>&1 || true
		wait "$fixture_pid" >/dev/null 2>&1 || true
	fi
	/bin/chmod -R u+w "$run_directory" >/dev/null 2>&1 || true
	/usr/bin/find "$run_directory" -depth -delete
}
trap cleanup EXIT HUP INT TERM

export GOCACHE="$run_directory/gocache"
export GOMODCACHE="$run_directory/gomodcache"

case "$(uname -m)" in
	arm64 | aarch64) architecture=arm64 ;;
	x86_64 | amd64) architecture=amd64 ;;
	*) printf 'unsupported load host architecture\n' >&2; exit 1 ;;
esac

cd "$root"
go run ./pkg/service/integration/reference-platform/cmd/platform-fixture \
	-certificate "$run_directory/ca.pem" \
	-ready "$run_directory/fixture-port" \
	>"$run_directory/fixture.log" 2>&1 &
fixture_pid=$!

attempt=0
while [ ! -s "$run_directory/fixture-port" ]; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 200 ] || ! kill -0 "$fixture_pid" 2>/dev/null; then
		cat "$run_directory/fixture.log" >&2
		printf 'TLS fixture did not become ready\n' >&2
		exit 1
	fi
	sleep 0.05
done
fixture_port=$(cat "$run_directory/fixture-port")

docker buildx create --name "$builder" --driver docker-container >/dev/null
docker buildx build \
	--builder "$builder" \
	--platform "linux/$architecture" \
	--file pkg/service/integration/reference-platform/Dockerfile \
	--load \
	--no-cache \
	--provenance=false \
	--tag "$image" \
	"$root" >/dev/null

docker run --detach \
	--name "$container" \
	--read-only \
	--user 65532:65532 \
	--cap-drop ALL \
	--security-opt no-new-privileges \
	--memory 64m \
	--cpus 0.25 \
	--pids-limit 64 \
	--ulimit nofile=256:256 \
	--tmpfs /tmp:rw,noexec,nosuid,size=16m,uid=65532,gid=65532,mode=0700 \
	--add-host host.docker.internal:host-gateway \
	--mount "type=bind,source=$run_directory/ca.pem,target=/etc/reference/ca.pem,readonly" \
	--env SSL_CERT_FILE=/etc/reference/ca.pem \
	--env "DEPENDENCY_URL=https://host.docker.internal:$fixture_port" \
	--publish 127.0.0.1::8080 \
	--publish 127.0.0.1::8081 \
	"$image" >/dev/null

business_port=$(docker port "$container" 8080/tcp | sed 's/.*://')
management_port=$(docker port "$container" 8081/tcp | sed 's/.*://')
attempt=0
until curl --fail --silent "http://127.0.0.1:$management_port/readyz" >/dev/null; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 200 ] || [ "$(docker inspect --format '{{.State.Running}}' "$container")" != true ]; then
		docker logs "$container" >&2
		printf 'load container did not become ready\n' >&2
		exit 1
	fi
	sleep 0.05
done

go run ./pkg/service/integration/reference-platform/cmd/platform-load \
	-endpoint "http://127.0.0.1:$business_port/" \
	-resources-endpoint "http://127.0.0.1:$business_port/resourcesz" \
	-requests 20000 \
	-concurrency 16 \
	-request-timeout 2s \
	-sample-interval 10ms \
	-overall-timeout 2m >"$run_directory/report.json"

jq -e '
	.requested == 20000 and
	.completed == 20000 and
	.failed == 0 and
	.sample_failures == 0 and
	.max_in_flight >= 1 and .max_in_flight <= 16 and
	.requests_per_second >= 500 and
	.p99_nanoseconds <= 250000000 and
	.max_heap_sys_bytes <= 33554432 and
	.max_goroutines <= 128 and
	.max_open_file_descriptors >= 1 and
	.max_open_file_descriptors <= 128
' "$run_directory/report.json" >/dev/null

curl --fail --silent "http://127.0.0.1:$management_port/readyz" >/dev/null
docker stop --time 5 "$container" >/dev/null
[ "$(docker inspect --format '{{.State.ExitCode}}' "$container")" = 143 ]
docker rm "$container" >/dev/null
container=""

cat "$run_directory/report.json"
printf 'reference constrained load campaign passed for linux/%s\n' "$architecture"
