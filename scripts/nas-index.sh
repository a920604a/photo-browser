#!/usr/bin/env bash
# Hourly incremental index, for DSM Task Scheduler.
#
# Paste this into Control Panel > Task Scheduler > Create > Scheduled Task >
# User-defined script, running as root, repeating every hour:
#
#   bash /volume1/docker/photo-browser/repo/scripts/nas-index.sh
#
# Exit 3 from photo-app means another index (or an admin command) holds the
# lock. That is the specified "already running" answer, not a failure, so this
# script reports it and exits 0 — otherwise DSM would email an alert every hour
# during a long initial scan.
set -uo pipefail

REPO="${REPO:-/volume1/docker/photo-browser/repo}"
COMPOSE="${COMPOSE:-deploy/compose/docker-compose.prod.yml}"
ENV_FILE="${ENV_FILE:-deploy/compose/.env.prod}"
LOG="${LOG:-/volume1/docker/photo-browser/data/index.log}"

while [ $# -gt 0 ]; do
  case "$1" in
    --compose) COMPOSE="$2"; shift 2 ;;
    --env-file) ENV_FILE="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

cd "$REPO" || { echo "repo not found: $REPO" >&2; exit 1; }
mkdir -p "$(dirname "$LOG")"

# Redirect the whole script rather than a { } block: a block is a subshell in
# some shells and rc would not survive it — and distinguishing exit 3 from a
# real failure is this script's entire job.
exec >> "$LOG" 2>&1

echo "=== $(date -Iseconds) index start ==="
compose_args=(-f "$COMPOSE")
if [ -f "$ENV_FILE" ] && [ -s "$ENV_FILE" ]; then
  compose_args+=(--env-file "$ENV_FILE")
fi
docker compose "${compose_args[@]}" run --rm --no-deps photo-app index
rc=$?
case "$rc" in
  0) echo "=== index ok ===" ;;
  3) echo "=== index skipped: another run holds the lock ===" ;;
  *) echo "=== index FAILED (exit $rc) ===" ;;
esac
echo

# Keep the log bounded; DSM does not rotate script output for us.
if [ -f "$LOG" ] && [ "$(wc -c < "$LOG")" -gt 5242880 ]; then
  tail -c 2097152 "$LOG" > "$LOG.tmp" && mv "$LOG.tmp" "$LOG"
fi

# 3 is the specified "already running" answer, not a failure.
[ "$rc" = 3 ] && exit 0
exit "$rc"
