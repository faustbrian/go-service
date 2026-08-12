#!/bin/sh
set -eu

root="$(cd "$(dirname "$0")/../../../.." && pwd)"
run_directory="$(mktemp -d /tmp/golib-reference-platform.XXXXXX)"
builder="golib-reference-platform-$$"
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
    docker buildx rm --force "${builder}" >/dev/null 2>&1 || true
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

cd "${root}"
go run ./pkg/service/integration/reference-platform/cmd/platform-fixture \
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

docker buildx create --name "${builder}" --driver docker-container >/dev/null

for architecture in amd64 arm64; do
    image="golib-reference-platform:${architecture}-$$"
    container="golib-reference-platform-${architecture}-$$"
    images="${images} ${image}"
    containers="${containers} ${container}"

    docker buildx build \
        --builder "${builder}" \
        --platform "linux/${architecture}" \
        --file pkg/service/integration/reference-platform/Dockerfile \
        --load \
        --no-cache \
        --provenance=false \
        --tag "${image}" \
        "${root}" >/dev/null

    actual_architecture="$(docker image inspect --format '{{.Architecture}}' "${image}")"
    [ "${actual_architecture}" = "${architecture}" ]
    [ "$(docker image inspect --format '{{.Config.User}}' "${image}")" = "65532:65532" ]

    docker run --detach \
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
        "${image}" >/dev/null

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

printf 'reference platform matrix passed for linux/amd64 and linux/arm64\n'
