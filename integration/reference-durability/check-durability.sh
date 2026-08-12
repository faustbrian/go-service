#!/bin/sh
set -eu

repo_root=$(CDPATH='' cd -- "$(dirname -- "$0")/../../../.." && pwd)
# shellcheck source=/dev/null
. "$repo_root/.golib/versions.env"
run_id="golib-reference-durability-$$"
postgres_container="$run_id-postgres"
valkey_container="$run_id-valkey"
network="$run_id-network"
cache_root=$(mktemp -d "${TMPDIR:-/tmp}/$run_id-cache.XXXXXX")

cleanup() {
	docker rm -f "$postgres_container" "$valkey_container" >/dev/null 2>&1 || true
	docker network rm "$network" >/dev/null 2>&1 || true
	/bin/chmod -R u+w "$cache_root" 2>/dev/null || true
	/usr/bin/find "$cache_root" -depth -delete
}
trap cleanup EXIT INT TERM

docker network create "$network" >/dev/null
docker run -d --name "$postgres_container" --network "$network" -p 127.0.0.1::5432 \
	-e POSTGRES_DB=reference -e POSTGRES_USER=reference -e POSTGRES_PASSWORD=reference \
	"$POSTGRES_IMAGE" >/dev/null
docker run -d --name "$valkey_container" --network "$network" -p 127.0.0.1::6379 \
	"$VALKEY_IMAGE" >/dev/null

attempt=0
until docker exec "$postgres_container" pg_isready -U reference -d reference >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	[ "$attempt" -lt 60 ] || { echo "PostgreSQL did not become ready" >&2; exit 1; }
	sleep 1
done
attempt=0
until docker exec "$valkey_container" valkey-cli ping >/dev/null 2>&1; do
	attempt=$((attempt + 1))
	[ "$attempt" -lt 60 ] || { echo "Valkey did not become ready" >&2; exit 1; }
	sleep 1
done

postgres_port=$(docker port "$postgres_container" 5432/tcp | awk -F: 'NR == 1 { print $NF }')
valkey_port=$(docker port "$valkey_container" 6379/tcp | awk -F: 'NR == 1 { print $NF }')
[ -n "$postgres_port" ] || { echo "PostgreSQL host port is unavailable" >&2; exit 1; }
[ -n "$valkey_port" ] || { echo "Valkey host port is unavailable" >&2; exit 1; }

cd "$repo_root"
DATABASE_URL="postgres://reference:reference@127.0.0.1:$postgres_port/reference?sslmode=disable" \
VALKEY_ADDRESS="127.0.0.1:$valkey_port" \
GOCACHE="$cache_root/build" GOMODCACHE="$cache_root/mod" \
	go test -tags=integration ./pkg/service/integration/reference-durability -run TestPostgresAndValkeyDurabilityComposition -count=1

echo "reference durability composition passed"
