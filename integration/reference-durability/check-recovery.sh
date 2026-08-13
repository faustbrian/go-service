#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../../../.." && pwd)
# shellcheck source=/dev/null
. "$repo_root/.golib/versions.env"
run_id="golib-reference-recovery-$$"
postgres_container="$run_id-postgres"
valkey_container="$run_id-valkey"
postgres_volume="$run_id-postgres"
valkey_volume="$run_id-valkey"
run_directory=$(mktemp -d "${TMPDIR:-/tmp}/$run_id.XXXXXX")
prepare_pid=""

cleanup() {
	if [ -n "$prepare_pid" ]; then
		kill -KILL "$prepare_pid" >/dev/null 2>&1 || true
		wait "$prepare_pid" >/dev/null 2>&1 || true
	fi
	docker rm --force "$postgres_container" "$valkey_container" >/dev/null 2>&1 || true
	docker volume rm --force "$postgres_volume" "$valkey_volume" >/dev/null 2>&1 || true
	/bin/chmod -R u+w "$run_directory" 2>/dev/null || true
	/usr/bin/find "$run_directory" -depth -delete
}
trap cleanup EXIT HUP INT TERM

export GOCACHE="$run_directory/gocache"
export GOMODCACHE="$run_directory/gomodcache"
probe="$run_directory/recovery-probe"
expectation="$run_directory/expectation.json"
database_url=""
valkey_address=""

start_postgres() {
	docker run --detach --name "$postgres_container" \
		--mount "type=volume,source=$postgres_volume,target=/var/lib/postgresql" \
		--publish 127.0.0.1::5432 \
		--env POSTGRES_DB=reference \
		--env POSTGRES_USER=reference \
		--env POSTGRES_PASSWORD=reference \
		"$POSTGRES_IMAGE" >/dev/null
	attempt=0
	until docker exec "$postgres_container" pg_isready -U reference -d reference >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 60 ] || [ "$(docker inspect --format '{{.State.Running}}' "$postgres_container")" != true ]; then
			docker logs "$postgres_container" >&2
			printf 'recovery PostgreSQL did not become ready\n' >&2
			exit 1
		fi
		sleep 1
	done
	postgres_port=$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 { print $NF }')
	[ -n "$postgres_port" ] || { printf 'recovery PostgreSQL port is unavailable\n' >&2; exit 1; }
	database_url="postgres://reference:reference@127.0.0.1:$postgres_port/reference?sslmode=disable"
}

start_valkey() {
	docker run --detach --name "$valkey_container" \
		--mount "type=volume,source=$valkey_volume,target=/data" \
		--publish 127.0.0.1::6379 \
		"$VALKEY_IMAGE" valkey-server --appendonly yes --appendfsync always >/dev/null
	attempt=0
	until docker exec "$valkey_container" valkey-cli ping >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 60 ] || [ "$(docker inspect --format '{{.State.Running}}' "$valkey_container")" != true ]; then
			docker logs "$valkey_container" >&2
			printf 'recovery Valkey did not become ready\n' >&2
			exit 1
		fi
		sleep 1
	done
	valkey_port=$(docker port "$valkey_container" 6379/tcp | awk -F: 'NR == 1 { print $NF }')
	[ -n "$valkey_port" ] || { printf 'recovery Valkey port is unavailable\n' >&2; exit 1; }
	valkey_address="127.0.0.1:$valkey_port"
}

recover() {
	"$probe" -mode recover \
		-database-url "$database_url" \
		-valkey-address "$valkey_address" \
		-stream reference-recovery \
		-expectation "$expectation" \
		-timeout 12s
}

docker volume create "$postgres_volume" >/dev/null
docker volume create "$valkey_volume" >/dev/null
start_postgres
start_valkey

cd "$repo_root"
go build -trimpath -o "$probe" \
	./pkg/service/integration/reference-durability/cmd/recovery-probe

"$probe" -mode prepare \
	-database-url "$database_url" \
	-valkey-address "$valkey_address" \
	-stream reference-recovery \
	-expectation "$expectation" \
	-timeout 30s >"$run_directory/prepare.log" 2>&1 &
prepare_pid=$!

attempt=0
until [ -s "$expectation" ] && jq -e \
	'.envelope_id != "" and .task_id == .envelope_id and .task_key == "reference-command-1"' \
	"$expectation" >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	if [ "$attempt" -ge 300 ] || ! kill -0 "$prepare_pid" 2>/dev/null; then
		cat "$run_directory/prepare.log" >&2
		printf 'recovery process did not reach the unacknowledged boundary\n' >&2
		exit 1
	fi
	sleep 0.1
done

kill -KILL "$prepare_pid"
set +e
wait "$prepare_pid"
prepare_status=$?
set -e
prepare_pid=""
[ "$prepare_status" -eq 137 ] || {
	printf 'recovery process exit status %s, want 137\n' "$prepare_status" >&2
	exit 1
}

docker kill "$postgres_container" "$valkey_container" >/dev/null
docker rm "$postgres_container" "$valkey_container" >/dev/null

if recover >"$run_directory/postgres-outage.log" 2>&1; then
	printf 'recovery unexpectedly succeeded while PostgreSQL was unavailable\n' >&2
	exit 1
fi
grep -F 'open recovery PostgreSQL' "$run_directory/postgres-outage.log" >/dev/null

start_postgres
if recover >"$run_directory/valkey-outage.log" 2>&1; then
	printf 'recovery unexpectedly succeeded while Valkey was unavailable\n' >&2
	exit 1
fi
grep -E 'create replacement-process recovery worker|reclaim recovery task' \
	"$run_directory/valkey-outage.log" >/dev/null

start_valkey
recover >"$run_directory/recovery.json"
jq -e '
	.replay_outcome == "replayed" and
	.business_rows == 1 and
	.outbox_state == "delivered" and
	.task_id != "" and
	.task_key == "reference-command-1" and
	.reclaimed == true and
	.acknowledged == true
' "$run_directory/recovery.json" >/dev/null

docker kill "$valkey_container" >/dev/null
docker rm "$valkey_container" >/dev/null
start_valkey
"$probe" -mode verify-ack \
	-database-url "$database_url" \
	-valkey-address "$valkey_address" \
	-stream reference-recovery \
	-timeout 12s >"$run_directory/acknowledgement.json"
jq -e '.acknowledgement_persisted == true' "$run_directory/acknowledgement.json" >/dev/null

cat "$run_directory/recovery.json"
printf 'reference process-death and dependency-replacement recovery passed\n'
