#!/bin/sh
set -eu

build_only=false
case "${1:-}" in
    '') [ "$#" -eq 0 ] || exit 2 ;;
    --build-only) [ "$#" -eq 1 ] || exit 2; build_only=true ;;
    *) printf 'usage: %s [--build-only]\n' "$0" >&2; exit 2 ;;
esac
module_directory=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
run_directory=$(mktemp -d "${TMPDIR:-/tmp}/golib-queue-skew.XXXXXXXXXXXX")
run_id=$(printf '%s' "${run_directory##*/}" | tr '[:upper:]' '[:lower:]')
label_key=com.faustbrian.golib.queue-skew.run
owner_label="$label_key=$run_id"
volume="$run_id-data"
volume_created=false
container=''
image_acquired=false
holder_pid=''
old_pid=''
new_pid=''
generation=0
valkey_image=$(awk 'NR == 1 && $1 == "9.1.0" && NF == 2 {print $2}' "$module_directory/testdata/valkey-image.txt")

cleanup() {
    status=$?
    trap - EXIT HUP INT TERM
    for pid in "$holder_pid" "$old_pid" "$new_pid"; do
        if [ -n "$pid" ]; then
            kill -KILL "$pid" >/dev/null 2>&1 || true
            wait "$pid" >/dev/null 2>&1 || true
        fi
    done
    if [ -n "$container" ]; then docker rm --force "$container" >/dev/null || status=1; fi
    if [ "$volume_created" = true ]; then
        owner=$(docker volume inspect --format "{{ index .Labels \"$label_key\" }}" "$volume") || owner=''
        if [ "$owner" = "$run_id" ]; then
            docker volume rm "$volume" >/dev/null || status=1
        else
            printf 'queue skew: refusing volume cleanup without run ownership\n' >&2
            status=1
        fi
    fi
    if [ "$image_acquired" = true ]; then docker image rm "$valkey_image" >/dev/null || status=1; fi
    chmod -R u+w "$run_directory" || status=1
    find "$run_directory" -depth -delete || status=1
    exit "$status"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

export GOENV=off GOWORK=off GOTOOLCHAIN=local GOFLAGS=-mod=readonly
export GOPROXY=https://proxy.golang.org GOSUMDB=sum.golang.org
export GOPRIVATE='' GONOPROXY='' GONOSUMDB=''
export GOCACHE="$run_directory/build" GOMODCACHE="$run_directory/mod" GOTMPDIR="$run_directory/tmp"
mkdir -p "$GOCACHE" "$GOMODCACHE" "$GOTMPDIR"
case "$valkey_image" in
    valkey/valkey:9.1.0-alpine@sha256:????????????????????????????????????????????????????????????????) ;;
    *) printf 'queue skew: invalid pinned Valkey identity\n' >&2; exit 1 ;;
esac

build_consumer() {
    version=$1
    source=$2
    directory="$run_directory/$version"
    mkdir "$directory"
    cp "$module_directory/cmd/queue-skew-probe/main.go" "$directory/main.go"
    printf 'module queue-skew-consumer.test\n\ngo 1.27.0\n\nrequire github.com/faustbrian/go-queue %s\n' "$version" >"$directory/go.mod"
    (
        cd "$directory"
        go mod download -json "github.com/faustbrian/go-queue@$version" > module.json
        jq -e --arg version "$version" --arg source "$source" \
            '.Path == "github.com/faustbrian/go-queue" and .Version == $version and .Origin.Hash == $source and (.Sum | startswith("h1:")) and (.GoModSum | startswith("h1:"))' module.json >/dev/null
        printf 'authenticated Queue %s source %s\n' "$version" "$source"
        go mod tidy
        go build -trimpath -o consumer .
        go version -m ./consumer
        ./consumer -mode identity -queue-version "$version"
    )
}
build_consumer v1.1.0 29797a97f747555d0cd00aa38dfe11f22e08f456
build_consumer v1.1.2 2173052b630557f345d02ec6961594218d47a316
[ "$build_only" = false ] || exit 0
old="$run_directory/v1.1.0/consumer"
new="$run_directory/v1.1.2/consumer"

start_valkey() {
    generation=$((generation + 1))
    container=$(docker create --name "$run_id-valkey-$generation" --label "$owner_label" \
        --mount "type=volume,source=$volume,target=/data" --publish 127.0.0.1::6379 \
        "$valkey_image" valkey-server --appendonly yes --appendfsync always)
    docker start "$container" >/dev/null
    attempt=0
    until docker exec "$container" valkey-cli ping >/dev/null 2>&1; do
        attempt=$((attempt + 1))
        [ "$attempt" -lt 60 ] || { printf 'queue skew: Valkey did not become ready\n' >&2; exit 1; }
        sleep 0.1
    done
    actual=$(docker exec "$container" valkey-cli INFO server | awk -F: '/^valkey_version:/ {gsub("\r", "", $2); print $2}')
    [ "$actual" = 9.1.0 ] || { printf 'queue skew: unexpected backend version\n' >&2; exit 1; }
    port=$(docker port "$container" 6379/tcp | awk -F: 'NR == 1 {print $NF}')
    [ -n "$port" ] || { printf 'queue skew: backend port unavailable\n' >&2; exit 1; }
    address="127.0.0.1:$port"
}

wait_ready() {
    path=$1
    pid=$2
    attempt=0
    until [ -s "$path" ] && jq -e '.ids | length == 1' "$path" >/dev/null 2>&1; do
        attempt=$((attempt + 1))
        if [ "$attempt" -ge 200 ] || ! kill -0 "$pid" 2>/dev/null; then
            printf 'queue skew: consumer did not reach its held-claim boundary\n' >&2
            exit 1
        fi
        sleep 0.05
    done
}

if docker volume inspect "$volume" >/dev/null 2>&1; then
    printf 'queue skew: refusing preexisting volume\n' >&2
    exit 1
fi
docker volume create --label "$owner_label" "$volume" >/dev/null
volume_created=true
if ! docker image inspect "$valkey_image" >/dev/null 2>&1; then image_acquired=true; fi
start_valkey

# Reclaim only after the previous owner has been killed or gracefully drained.
for direction in upgrade rollback; do
    if [ "$direction" = upgrade ]; then
        producer="$old"; producer_version=v1.1.0; replacement="$new"; replacement_version=v1.1.2
    else
        producer="$new"; producer_version=v1.1.2; replacement="$old"; replacement_version=v1.1.0
    fi
    stream="$run_id-$direction"
    "$producer" -queue-version "$producer_version" -mode publish -address "$address" -stream "$stream" -prefix "$direction"
    ready="$run_directory/$direction-ready.json"
    "$producer" -queue-version "$producer_version" -mode hold -address "$address" -stream "$stream" -prefix "$direction" \
        -consumer "$direction-abandoned" -ready "$ready" -timeout 30s >"$run_directory/$direction-owner.log" 2>&1 &
    holder_pid=$!
    wait_ready "$ready" "$holder_pid"
    jq -e --arg id "$direction-0" '.ids == [$id] and .acknowledged == 0 and .pending == 1 and .lag == 0' "$ready" >/dev/null
    if [ "$direction" = upgrade ]; then kill -KILL "$holder_pid"; else kill -TERM "$holder_pid"; fi
    set +e
    wait "$holder_pid"
    owner_status=$?
    set -e
    holder_pid=''
    if [ "$direction" = upgrade ]; then [ "$owner_status" -eq 137 ]; else [ "$owner_status" -eq 0 ]; fi
    "$replacement" -queue-version "$replacement_version" -mode consume -address "$address" -stream "$stream" -prefix "$direction" \
        -consumer "$direction-replacement" -reclaim >"$run_directory/$direction.json"
    jq -e --arg id "$direction-0" '.ids == [$id] and .acknowledged == 1 and .reclaimed == 1 and .pending == 0 and .lag == 0' "$run_directory/$direction.json" >/dev/null
    cat "$run_directory/$direction.json"
done

stream="$run_id-mixed"
"$old" -queue-version v1.1.0 -mode publish -address "$address" -stream "$stream" -prefix mixed -count 4
"$new" -queue-version v1.1.2 -mode publish -address "$address" -stream "$stream" -prefix mixed -first 4 -count 4
"$old" -queue-version v1.1.0 -mode compete -address "$address" -stream "$stream" -prefix mixed -consumer mixed-old -count 8 \
    -ready "$run_directory/old-ready.json" -release "$run_directory/release" >"$run_directory/mixed-old.json" 2>"$run_directory/mixed-old.log" &
old_pid=$!
"$new" -queue-version v1.1.2 -mode compete -address "$address" -stream "$stream" -prefix mixed -consumer mixed-new -count 8 \
    -ready "$run_directory/new-ready.json" -release "$run_directory/release" >"$run_directory/mixed-new.json" 2>"$run_directory/mixed-new.log" &
new_pid=$!
wait_ready "$run_directory/old-ready.json" "$old_pid"
wait_ready "$run_directory/new-ready.json" "$new_pid"
touch "$run_directory/release"
set +e
wait "$old_pid"
old_status=$?
set -e
old_pid=''
[ "$old_status" -eq 0 ]
set +e
wait "$new_pid"
new_status=$?
set -e
new_pid=''
[ "$new_status" -eq 0 ]
jq -se 'all(.[]; (.ids | length) > 0 and .acknowledged == (.ids | length) and .reclaimed == 0) and
    ([.[].ids[]] | sort) == ["mixed-0","mixed-1","mixed-2","mixed-3","mixed-4","mixed-5","mixed-6","mixed-7"]' \
    "$run_directory/mixed-old.json" "$run_directory/mixed-new.json"
cat "$run_directory/mixed-old.json" "$run_directory/mixed-new.json"

docker kill "$container" >/dev/null
docker rm "$container" >/dev/null
container=''
start_valkey
for cohort in upgrade rollback mixed; do
    expected=1
    [ "$cohort" != mixed ] || expected=8
    retained=$(docker exec "$container" valkey-cli --raw XLEN "$run_id-$cohort")
    [ "$retained" = "$expected" ] || { printf 'queue skew: stream data was lost on backend replacement\n' >&2; exit 1; }
    pending=$(docker exec "$container" valkey-cli --raw XPENDING "$run_id-$cohort" released-queue-skew | awk 'NR == 1 {print}')
    [ "$pending" = 0 ] || { printf 'queue skew: acknowledged consumer group was lost or has pending work\n' >&2; exit 1; }
    "$old" -queue-version v1.1.0 -mode verify-ack -address "$address" -stream "$run_id-$cohort" -prefix "$cohort"
    "$new" -queue-version v1.1.2 -mode verify-ack -address "$address" -stream "$run_id-$cohort" -prefix "$cohort"
done
printf 'released Queue v1.1.0/v1.1.2 stopped-owner handoff, common-fields rollback, bounded competition and acknowledgement persistence passed\n'
