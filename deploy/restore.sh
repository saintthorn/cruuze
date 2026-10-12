#!/bin/sh
# Restores a backup made by backup.sh over the running install. Everything
# in the database and the attachments volume is replaced.
#
#   ./restore.sh backups/2026-10-01T031500Z
set -eu

cd "$(dirname "$0")"
src="${1:?usage: ./restore.sh <backup directory>}"
[ -s "$src/database.dump" ] || { echo "$src/database.dump not found" >&2; exit 1; }

printf 'This replaces the current database and attachments with %s. Type "restore" to go on: ' "$src"
read -r answer
[ "$answer" = "restore" ] || { echo "nothing changed"; exit 1; }

# Stop the app so nothing writes while the data is swapped.
docker compose stop app
docker compose exec -T postgres pg_restore -U cruuze -d cruuze --clean --if-exists --no-owner < "$src/database.dump"
if [ -s "$src/attachments.tar.gz" ]; then
	docker run --rm -i -v cruuze_appdata:/data alpine sh -c 'find /data -mindepth 1 -delete && tar -C /data -xzf -' < "$src/attachments.tar.gz"
fi
docker compose start app
echo "restored from $src"
