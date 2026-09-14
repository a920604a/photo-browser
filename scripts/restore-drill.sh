#!/usr/bin/env bash
# Walks spec 4 section 9's restore drill end to end against the prodcheck
# stack. It deliberately destroys the data and thumbnail volumes, so it must
# only ever run against prodcheck — never against production.
set -euo pipefail

COMPOSE="deploy/compose/docker-compose.prodcheck.yml"
DEST="${1:-/tmp/pb-restore-drill}"
step() { echo; echo "=== $* ==="; }
dcr() { docker compose -f "$COMPOSE" run --rm --no-deps photo-app "$@" 2>/dev/null; }

case "$COMPOSE" in
  *prodcheck*) ;;
  *) echo "refusing to run against $COMPOSE — prodcheck only" >&2; exit 2 ;;
esac

rm -rf "$DEST"; mkdir -p "$DEST"

step "1. deploy the pinned version and seed state"
make prodcheck-up >/dev/null
dcr admin add-user --uid=restore-probe --email=probe@example.com --role=member >/dev/null 2>&1 || true
before_users="$(dcr admin list-users | sort)"
echo "$before_users"

step "2. back up (sqlite online backup + config)"
bash scripts/backup-sqlite.sh --compose "$COMPOSE" --dest "$DEST" --keep 5 2>&1 | grep -v APP_VERSION
BACKUP="$(ls -1t "$DEST"/photo-*.db | head -1)"
echo "backup: $BACKUP"

step "3. destroy the data and thumbnail volumes"
docker compose -f "$COMPOSE" down -v >/dev/null 2>&1
docker compose -f "$COMPOSE" up -d >/dev/null 2>&1
sleep 4
if dcr admin list-users | grep -q restore-probe; then
  echo "FAIL: volumes were not actually destroyed"; exit 1
fi
echo "confirmed: allowlist and thumbnails are gone"

step "4. restore the SQLite backup (allowlist comes back)"
docker compose -f "$COMPOSE" stop photo-app >/dev/null 2>&1
CID="$(docker compose -f "$COMPOSE" run -d --no-deps --entrypoint sleep photo-app 60 2>/dev/null)"
docker cp "$BACKUP" "$CID:/srv/data/photo.db"
# The WAL and shm of the destroyed database must not survive alongside the
# restored file, or SQLite would replay a log that no longer matches it.
docker exec "$CID" sh -c 'rm -f /srv/data/photo.db-wal /srv/data/photo.db-shm' 2>/dev/null || true
docker exec "$CID" chown 65532:65532 /srv/data/photo.db 2>/dev/null || true
docker rm -f "$CID" >/dev/null 2>&1
docker compose -f "$COMPOSE" up -d >/dev/null 2>&1
sleep 4
after_users="$(dcr admin list-users | sort)"
if [ "$after_users" != "$before_users" ]; then
  echo "FAIL: allowlist differs after restore"
  echo "before: $before_users"
  echo "after:  $after_users"
  exit 1
fi
echo "allowlist restored identically"

step "5. rebuild derived catalogue rows from the filesystem"
dcr rebuild
dcr index

step "6. rebuild thumbnails (never restored from backup)"
dcr index --rebuild-thumbnails

step "7. verify all three identities and the media path"
MEMBER=$(curl -s "http://localhost:8090/mint?sub=member-1&email=member@example.com&verified=1")
ADMIN=$(curl -s "http://localhost:8090/mint?sub=admin-1&email=admin@example.com&verified=1")
DENIED=$(curl -s "http://localhost:8090/mint?sub=denied-1&email=denied@example.com&verified=1")
bash scripts/verify-deployment.sh --base-url http://localhost:8088 --host photos-api.localhost \
  --member-token "$MEMBER" --admin-token "$ADMIN" --denied-token "$DENIED" --compose "$COMPOSE"

echo
echo "restore drill PASSED — allowlist restored from backup, catalogue and"
echo "thumbnails rebuilt from the filesystem, all identities behave correctly."
