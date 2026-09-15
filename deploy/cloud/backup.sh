#!/usr/bin/env bash
# Nightly backup of the hosted console, as docs/trust/backup-restore.md
# describes it: a logical dump of the database and a tarball of each of the
# two volumes the container engine owns. Runs unattended from the systemd
# timer deploy/cloud/systemd/abhed-backup.timer installs; also fine by hand.
#
#   ./deploy/cloud/backup.sh                 # into ~/backups
#   ABHED_BACKUP_DIR=/mnt/backups ./deploy/cloud/backup.sh
#
# What it does NOT back up, on purpose: the Postgres data directory
# (abhed-db-data) — pg_dump reads it live and the dump is consistent and
# version-portable where a raw copy is neither — and the two password files
# beside deploy/config.json, which are regenerable secrets, not data.
#
# Every container name and volume name defaults to what deploy/run.sh uses;
# override with the same ABHED_* variables run.sh reads.
set -euo pipefail

RUNTIME="${ABHED_RUNTIME:-podman}"
DB_NAME="${ABHED_DB_CONTAINER:-abhed-db}"
VOLUME="${ABHED_VOLUME:-abhed-workspace}"
STATE_VOLUME="${ABHED_STATE_VOLUME:-abhed-state}"
# The image that tars the volumes. The Postgres image is already present on
# every deployment run.sh set up, has tar, and pulling nothing new means a
# backup never fails because a registry was unreachable at 03:00.
HELPER_IMAGE="${ABHED_DB_IMAGE:-docker.io/library/postgres:16-alpine}"
# abhed_admin on a deployment run.sh created; a data directory carried over
# from the first version of run.sh has `abhed` as its superuser instead.
DB_ADMIN_ROLE="${ABHED_DB_ADMIN_ROLE:-abhed_admin}"
DEST="${ABHED_BACKUP_DIR:-$HOME/backups}"
# Days of backups to keep locally. Each night is roughly the size of the
# database plus the workspace; fourteen of them is a fortnight of choices.
KEEP_DAYS="${ABHED_BACKUP_KEEP_DAYS:-14}"
# Optional off-host copy: an rclone remote such as "r2:abhed-backups". The
# host's disk is the thing most likely to be lost, so a backup that lives only
# on it is a backup of the wrong failure.
REMOTE="${ABHED_BACKUP_RCLONE_REMOTE:-}"

STAMP="$(date -u +%Y-%m-%dT%H%M%SZ)"
mkdir -p "$DEST"
chmod 700 "$DEST"

command -v "$RUNTIME" >/dev/null 2>&1 || { echo "error: $RUNTIME not found" >&2; exit 1; }
"$RUNTIME" container exists "$DB_NAME" 2>/dev/null || { echo "error: no $DB_NAME container" >&2; exit 1; }

echo "backup $STAMP → $DEST"

# 1. Database. pg_dump writes the custom-format archive to stdout so nothing
#    lands in the container's filesystem and no `cp` step is needed. Written
#    to a temporary name and renamed at the end, so a half-written file left
#    by a failure never looks like a backup.
DUMP="$DEST/abhed-$STAMP.dump"
"$RUNTIME" exec "$DB_NAME" pg_dump -U "$DB_ADMIN_ROLE" -d abhed --format=custom > "$DUMP.part"
mv "$DUMP.part" "$DUMP"
echo "  database   $(du -h "$DUMP" | cut -f1)  $DUMP"

# 2. The two volumes, each tarred by a throwaway container that mounts the
#    volume read-only and the destination directory read-write.
for pair in "$VOLUME:abhed-workspace" "$STATE_VOLUME:abhed-state"; do
  vol="${pair%%:*}"; label="${pair##*:}"
  out="$label-$STAMP.tgz"
  "$RUNTIME" run --rm \
    --volume "$vol":/data:ro \
    --volume "$DEST":/backup \
    "$HELPER_IMAGE" tar czf "/backup/$out.part" -C /data .
  mv "$DEST/$out.part" "$DEST/$out"
  echo "  $label $(du -h "$DEST/$out" | cut -f1)  $DEST/$out"
done

# 3. Off-host copy, if configured. `copy` never deletes at the remote, so a
#    compromised host cannot use this job to erase the off-site history.
if [ -n "$REMOTE" ]; then
  if command -v rclone >/dev/null 2>&1; then
    rclone copy --include "*-$STAMP.*" "$DEST" "$REMOTE" && echo "  copied to $REMOTE"
  else
    echo "  warning: ABHED_BACKUP_RCLONE_REMOTE set but rclone is not installed" >&2
  fi
fi

# 4. Retention, local only.
find "$DEST" -maxdepth 1 -type f \( -name 'abhed-*.dump' -o -name 'abhed-*.tgz' \) \
  -mtime +"$KEEP_DAYS" -print -delete | sed 's/^/  removed /'

echo "done"
