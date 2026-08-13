#!/bin/sh
set -eu

module_directory=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
repo_root=$(CDPATH='' cd -- "$module_directory/../../../.." && pwd)
postgres_matrix="$module_directory/testdata/postgres-images.tsv"
valkey_matrix="$module_directory/testdata/valkey-image.txt"
run_id="golib-reference-version-matrix-$$"
label_key='com.faustbrian.golib.reference-durability.run'
owner_label="$label_key=$run_id"
network="$run_id-network"
run_directory=$(mktemp -d "${TMPDIR:-/tmp}/$run_id.XXXXXX")
new_images="$run_directory/new-images"
active_postgres=''
active_valkey=''

container_owned() {
	name=$1
	actual=$(docker inspect --format "{{ index .Config.Labels \"$label_key\" }}" "$name" 2>/dev/null || true)
	[ "$actual" = "$run_id" ]
}

network_owned() {
	actual=$(docker network inspect --format "{{ index .Labels \"$label_key\" }}" "$network" 2>/dev/null || true)
	[ "$actual" = "$run_id" ]
}

remove_container() {
	name=$1
	if [ -n "$name" ] && container_owned "$name"; then
		docker rm --force "$name" >/dev/null
	fi
}

track_image() {
	image=$1
	if ! docker image inspect "$image" >/dev/null 2>&1; then
		printf '%s\n' "$image" >>"$new_images"
	fi
}

cleanup() {
	remove_container "$active_postgres" || true
	remove_container "$active_valkey" || true
	if network_owned; then
		docker network rm "$network" >/dev/null 2>&1 || true
	fi
	if [ -f "$new_images" ]; then
		while IFS= read -r image; do
			[ -z "$image" ] || docker image rm "$image" >/dev/null 2>&1 || true
		done <"$new_images"
	fi
	/bin/chmod -R u+w "$run_directory" 2>/dev/null || true
	/usr/bin/find "$run_directory" -depth -delete
}
trap cleanup EXIT HUP INT TERM

read -r valkey_version valkey_image valkey_extra <"$valkey_matrix"
if [ "$valkey_version" != '9.1.0' ] || [ -n "${valkey_extra:-}" ]; then
	printf 'invalid Valkey image identity\n' >&2
	exit 1
fi
case "$valkey_image" in
valkey/valkey:9.1.0-alpine@sha256:*) ;;
*)
	printf 'Valkey image is not digest-pinned\n' >&2
	exit 1
	;;
esac

mkdir "$run_directory/gocache" "$run_directory/gomodcache"
touch "$new_images"
docker network create --label "$owner_label" "$network" >/dev/null

expected_version=14
while read -r postgres_version postgres_image postgres_extra; do
	if [ -z "$postgres_version" ] || [ -z "$postgres_image" ] || [ -n "${postgres_extra:-}" ]; then
		printf 'invalid PostgreSQL matrix row for version %s\n' "${postgres_version:-unknown}" >&2
		exit 1
	fi
	if [ "$postgres_version" -ne "$expected_version" ]; then
		printf 'expected PostgreSQL %s, found %s\n' "$expected_version" "$postgres_version" >&2
		exit 1
	fi
	case "$postgres_image" in
	postgres:"$postgres_version".*-alpine@sha256:*) ;;
	*)
		printf 'PostgreSQL %s image is not digest-pinned\n' "$postgres_version" >&2
		exit 1
		;;
	esac

	active_postgres="$run_id-postgres-$postgres_version"
	active_valkey="$run_id-valkey-$postgres_version"
	track_image "$postgres_image"
	track_image "$valkey_image"
	docker run --detach --name "$active_postgres" --label "$owner_label" \
		--network "$network" --publish 127.0.0.1::5432 \
		--env POSTGRES_DB=reference --env POSTGRES_USER=reference \
		--env POSTGRES_PASSWORD=reference "$postgres_image" >/dev/null
	docker run --detach --name "$active_valkey" --label "$owner_label" \
		--network "$network" --publish 127.0.0.1::6379 "$valkey_image" >/dev/null

	attempt=0
	until docker exec "$active_postgres" pg_isready -U reference -d reference >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 60 ] || [ "$(docker inspect --format '{{.State.Running}}' "$active_postgres")" != true ]; then
			docker logs "$active_postgres" >&2
			printf 'PostgreSQL %s did not become ready\n' "$postgres_version" >&2
			exit 1
		fi
		sleep 1
	done
	attempt=0
	until docker exec "$active_valkey" valkey-cli ping >/dev/null 2>&1; do
		attempt=$((attempt + 1))
		if [ "$attempt" -ge 60 ] || [ "$(docker inspect --format '{{.State.Running}}' "$active_valkey")" != true ]; then
			docker logs "$active_valkey" >&2
			printf 'Valkey did not become ready for PostgreSQL %s\n' "$postgres_version" >&2
			exit 1
		fi
		sleep 1
	done

	actual_postgres=$(docker exec "$active_postgres" psql -U reference -d reference -Atc 'SHOW server_version_num')
	case "$actual_postgres" in
	"$postgres_version"????) ;;
	*)
		printf 'PostgreSQL server version %s does not match major %s\n' "$actual_postgres" "$postgres_version" >&2
		exit 1
		;;
	esac
	actual_valkey=$(docker exec "$active_valkey" valkey-cli INFO server | awk -F: '/^valkey_version:/ { gsub("\\r", "", $2); print $2 }')
	if [ "$actual_valkey" != "$valkey_version" ]; then
		printf 'Valkey server version %s does not match %s\n' "$actual_valkey" "$valkey_version" >&2
		exit 1
	fi

	postgres_port=$(docker port "$active_postgres" 5432/tcp | awk -F: 'NR == 1 { print $NF }')
	valkey_port=$(docker port "$active_valkey" 6379/tcp | awk -F: 'NR == 1 { print $NF }')
	if [ -z "$postgres_port" ] || [ -z "$valkey_port" ]; then
		printf 'backend host ports are unavailable for PostgreSQL %s\n' "$postgres_version" >&2
		exit 1
	fi

	(
		cd "$repo_root"
		DATABASE_URL="postgres://reference:reference@127.0.0.1:$postgres_port/reference?sslmode=disable" \
			VALKEY_ADDRESS="127.0.0.1:$valkey_port" \
			GOCACHE="$run_directory/gocache" GOMODCACHE="$run_directory/gomodcache" \
			go test -tags=integration ./pkg/service/integration/reference-durability \
			-run '^TestPostgresAndValkeyDurabilityComposition$' -count=1
	)
	printf 'reference durability composition passed on PostgreSQL %s and Valkey %s\n' \
		"$postgres_version" "$valkey_version"

	remove_container "$active_postgres"
	remove_container "$active_valkey"
	active_postgres=''
	active_valkey=''
	expected_version=$((expected_version + 1))
done <"$postgres_matrix"

if [ "$expected_version" -ne 19 ]; then
	printf 'PostgreSQL matrix must contain versions 14 through 18 exactly once\n' >&2
	exit 1
fi

printf 'reference durability PostgreSQL 14-18 matrix passed with Valkey %s\n' "$valkey_version"
