#!/bin/sh
# Backs up a Cruuze install made with deploy/compose.yml: the database and
# the attachments, into one dated directory. Safe to run while the app is up.
#
#   ./backup.sh [directory]        default: ./backups
#   KEEP=14 ./backup.sh            also delete backups older than 14 days
#
# From cron, daily at 03:15:
#   15 3 * * * cd /path/to/cruuze/deploy && KEEP=14 ./backup.sh >> backup.log 2>&1
set -eu

cd "$(dirname "$0")"
dest="${1:-backups}/$(date -u +%Y-%m-%dT%H%M%SZ)"
mkdir -p "$dest"

# A custom-format dump: compressed, and restorable table by table.
docker compose exec -T postgres pg_dump -U cruuze -d cruuze --format=custom > "$dest/database.dump"

# Attachments live in the app's volume unless S3 holds them. The app image
# has no shell, so a throwaway container reads the volume.
docker run --rm -v cruuze_appdata:/data:ro alpine tar -C /data -czf - . > "$dest/attachments.tar.gz"

# An empty or missing dump is not a backup.
if [ ! -s "$dest/database.dump" ]; then
	echo "backup failed: $dest/database.dump is empty" >&2
	exit 1
fi
echo "backed up to $dest ($(du -sh "$dest" | cut -f1))"

if [ -n "${KEEP:-}" ]; then
	find "$(dirname "$dest")" -mindepth 1 -maxdepth 1 -type d -mtime "+$KEEP" -exec rm -rf {} +
fi
