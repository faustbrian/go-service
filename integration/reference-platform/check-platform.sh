#!/bin/sh
set -eu

if [ "$#" -gt 1 ]; then
    printf 'usage: %s [amd64|arm64]\n' "$0" >&2
    exit 2
fi
architectures="amd64 arm64"
if [ "$#" -eq 1 ]; then
    case "$1:$(uname -m)" in
        amd64:x86_64 | amd64:amd64 | arm64:aarch64 | arm64:arm64) architectures="$1" ;;
        *) printf 'selected platform architecture must match the native amd64 or arm64 host\n' >&2; exit 2 ;;
    esac
fi

module_directory=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
root=$(CDPATH='' cd -- "$module_directory/../.." && pwd)
run_directory=$(mktemp -d "${TMPDIR:-/tmp}/golib-reference-platform.XXXXXXXXXXXX")
run_id=$(printf '%s' "${run_directory##*/}" | tr '[:upper:]' '[:lower:]')
builder="$run_id"
builder_created=false
buildkit_image="moby/buildkit@sha256:cec9f139f45e93c5c69c60f8b07cfad9f43f4ef6b6a6cd917527fea5ff2e3dea"
buildkit_image_acquired=false
fixture_pid=""
containers=""
images=""

cleanup() {
    for container in ${containers}; do
        docker rm --force "${container}" >/dev/null 2>&1 || true
    done
    for image in ${images}; do
        docker image rm --force "${image}" >/dev/null 2>&1 || true
    done
    if [ "$builder_created" = true ]; then
        docker buildx rm --force "${builder}" >/dev/null 2>&1 || true
    fi
    if [ "$buildkit_image_acquired" = true ]; then
        docker image rm "$buildkit_image" >/dev/null 2>&1 || true
    fi
    if [ -n "${fixture_pid}" ]; then
        kill "${fixture_pid}" >/dev/null 2>&1 || true
        wait "${fixture_pid}" >/dev/null 2>&1 || true
    fi
    chmod -R u+w "${run_directory}" >/dev/null 2>&1 || true
    find "${run_directory}" -depth -delete
}
trap cleanup EXIT HUP INT TERM

export GOCACHE="${run_directory}/gocache"
export GOMODCACHE="${run_directory}/gomodcache"
export GOTMPDIR="$run_directory/gotmpdir"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
export GOENV=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
export GOPRIVATE='' GONOPROXY='' GONOSUMDB=''

cd "$module_directory"
fixture="$run_directory/platform-fixture"
go build -trimpath -o "$fixture" ./cmd/platform-fixture
"$fixture" \
    -certificate "${run_directory}/ca.pem" \
    -ready "${run_directory}/fixture-port" \
    >"${run_directory}/fixture.log" 2>&1 &
fixture_pid="$!"

attempt=0
while [ ! -s "${run_directory}/fixture-port" ]; do
    attempt=$((attempt + 1))
    if [ "${attempt}" -ge 200 ] || ! kill -0 "${fixture_pid}" 2>/dev/null; then
        cat "${run_directory}/fixture.log" >&2
        printf 'TLS fixture did not become ready\n' >&2
        exit 1
    fi
    sleep 0.05
done
fixture_port="$(cat "${run_directory}/fixture-port")"

if ! docker image inspect "$buildkit_image" >/dev/null 2>&1; then
    buildkit_image_acquired=true
fi
docker buildx create --name "${builder}" --driver docker-container \
    --driver-opt "image=$buildkit_image" >/dev/null
builder_created=true

for architecture in $architectures; do
    image="$run_id:${architecture}"
    container="$run_id-${architecture}"
    if docker image inspect "$image" >/dev/null 2>&1; then
        printf 'refusing existing qualification image %s\n' "$image" >&2
        exit 1
    fi

    docker buildx build \
        --builder "${builder}" \
        --platform "linux/${architecture}" \
        --file "$module_directory/Dockerfile" \
        --load \
        --no-cache \
        --provenance=false \
        --tag "${image}" \
        "${root}" >/dev/null
    images="${images} ${image}"

    actual_architecture="$(docker image inspect --format '{{.Architecture}}' "${image}")"
    [ "${actual_architecture}" = "${architecture}" ]
    [ "$(docker image inspect --format '{{.Config.User}}' "${image}")" = "65532:65532" ]

    container_id=$(docker create \
        --name "${container}" \
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
        --mount "type=bind,source=${run_directory}/ca.pem,target=/etc/reference/ca.pem,readonly" \
        --env SSL_CERT_FILE=/etc/reference/ca.pem \
        --env "DEPENDENCY_URL=https://host.docker.internal:${fixture_port}" \
        --publish 127.0.0.1::8080 \
        --publish 127.0.0.1::8081 \
        "${image}")
    container="$container_id"
    containers="${containers} ${container}"
    docker start "$container" >/dev/null

    business_port="$(docker port "${container}" 8080/tcp | sed 's/.*://')"
    management_port="$(docker port "${container}" 8081/tcp | sed 's/.*://')"
    attempt=0
    until curl --fail --silent "http://127.0.0.1:${management_port}/readyz" >/dev/null; do
        attempt=$((attempt + 1))
        if [ "${attempt}" -ge 200 ] || [ "$(docker inspect --format '{{.State.Running}}' "${container}")" != "true" ]; then
            docker logs "${container}" >&2
            printf '%s container did not become ready\n' "${architecture}" >&2
            exit 1
        fi
        sleep 0.05
    done

    curl --fail --silent "http://127.0.0.1:${management_port}/livez" >/dev/null
    curl --fail --silent "http://127.0.0.1:${management_port}/startupz" >/dev/null
    [ "$(curl --output /dev/null --silent --write-out '%{http_code}' "http://127.0.0.1:${business_port}/dependencyz")" = "204" ]
    runtime_report="$(curl --fail --silent "http://127.0.0.1:${business_port}/runtimez")"
    printf '%s' "${runtime_report}" | jq -e \
        --arg architecture "${architecture}" \
        '.goos == "linux" and .goarch == $architecture and
         .effective_user_id == 65532 and .effective_group_id == 65532 and
         .temporary_storage == true' >/dev/null

    docker stop --time 5 "${container}" >/dev/null
    exit_code="$(docker inspect --format '{{.State.ExitCode}}' "${container}")"
    [ "${exit_code}" = "143" ]
    docker rm "${container}" >/dev/null
    containers="$(printf '%s' "${containers}" | sed "s# ${container}##")"
done

printf 'reference platform matrix passed for architectures: %s\n' "$architectures"
