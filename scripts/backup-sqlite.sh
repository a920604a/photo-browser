#!/usr/bin/env bash
# Daily backup: an online SQLite snapshot plus the deployment config needed to
# rebuild the stack. Thumbnails are derived data and are deliberately not
# backed up — `photo-app index --rebuild-thumbnails` regenerates them.
set -euo pipefail

COMPOSE=""; DEST=""; KEEP=7
while [ $# -gt 0 ]; do
  case "$1" in
    --compose) COMPOSE="$2"; shift 2 ;;
    --dest) DEST="$2"; shift 2 ;;
    --keep) KEEP="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done
[ -n "$COMPOSE" ] && [ -n "$DEST" ] || {
  echo "usage: backup-sqlite.sh --compose FILE --dest DIR [--keep N]" >&2; exit 2; }

STAMP="$(date -u +%Y%m%dT%H%M%SZ)"
mkdir -p "$DEST"
DEST_ABS="$(cd "$DEST" && pwd)"

# The snapshot is written inside the container, because only the container can
# see /srv/data, then copied out.
IN_CONTAINER="/srv/data/backup/photo-$STAMP.db"
echo "[backup] snapshotting to $IN_CONTAINER"
docker compose -f "$COMPOSE" run --rm --no-deps photo-app backup --out="$IN_CONTAINER"

CID="$(docker compose -f "$COMPOSE" run -d --no-deps --entrypoint sleep photo-app 60)"
trap 'docker rm -f "$CID" >/dev/null 2>&1 || true' EXIT
docker cp "$CID:$IN_CONTAINER" "$DEST_ABS/photo-$STAMP.db"
docker exec "$CID" rm -f "$IN_CONTAINER" 2>/dev/null || true
docker rm -f "$CID" >/dev/null 2>&1 || true
trap - EXIT

# Verify the copy opens as a database before trusting it. A backup nobody has
# opened is a backup nobody knows is broken.
if command -v sqlite3 >/dev/null 2>&1; then
  check="$(sqlite3 "$DEST_ABS/photo-$STAMP.db" 'PRAGMA integrity_check;')"
else
  # DSM has no sqlite3 binary; the api-acceptance image carries one.
  check="$(docker run --rm -v "$DEST_ABS":/b photo-browser-api-acceptance \
    sqlite3 "/b/photo-$STAMP.db" 'PRAGMA integrity_check;')"
fi
if [ "$(printf '%s' "$check" | tr -d '[:space:]')" = "ok" ]; then
  echo "[backup] integrity_check ok"
else
  echo "[backup] integrity_check FAILED: $check" >&2
  exit 1
fi

# Deployment config travels with the data; a restore needs both.
tar -czf "$DEST_ABS/config-$STAMP.tar.gz" \
  deploy/compose/docker-compose.prod.yml \
  deploy/nginx/nginx.prod.conf \
  deploy/cloudflared/config.yml \
  deploy/VERSION \
  docs/deploy/
echo "[backup] wrote config-$STAMP.tar.gz"

# Retention. Credentials are NOT in these archives — the tunnel credential is
# covered by the existing secret backup process (spec section 9).
for pattern in "photo-*.db" "config-*.tar.gz"; do
  # shellcheck disable=SC2012
  ls -1t "$DEST_ABS"/$pattern 2>/dev/null | tail -n +$((KEEP + 1)) | while read -r old; do
    echo "[backup] pruning $(basename "$old")"
    rm -f "$old"
  done
done

echo "[backup] done: $DEST_ABS/photo-$STAMP.db"
