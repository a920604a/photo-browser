#!/bin/bash
# End-to-end acceptance for the auth+api+media POC.
#
# Brings up backend + nginx + testauth via docker compose, seeds allowlist
# users, runs an indexer pass, and exercises the full HTTP surface plus a
# Nginx internal-media guard.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
COMPOSE_FILE="$ROOT/deploy/compose/docker-compose.acceptance.yml"
FIXTURE_SRC_DIR="$ROOT/test-photos/20170627阿里山二日遊"

fail() { echo "ACCEPTANCE-API FAIL: $*" >&2; docker compose -f "$COMPOSE_FILE" logs --no-color 2>&1 | tail -80 >&2 || true; exit 1; }

TMP_ROOT="$(mktemp -d)"
trap 'docker compose -f "$COMPOSE_FILE" down -v --remove-orphans >/dev/null 2>&1 || true; rm -rf "$TMP_ROOT"' EXIT

PHOTOS_DIR="$TMP_ROOT/photos"
ALBUM_DIR="$PHOTOS_DIR/travel/album1"
mkdir -p "$ALBUM_DIR"
for n in 1 2 3; do
  src="$FIXTURE_SRC_DIR/LINE_ALBUM_阿里山二日遊_231225_${n}.jpg"
  [[ -r "$src" ]] || fail "fixture missing: $src"
  cp "$src" "$ALBUM_DIR/photo_${n}.jpg"
done
export PHOTO_FIXTURES="$PHOTOS_DIR"

echo "===== [0] compose up ====="
docker compose -f "$COMPOSE_FILE" up -d >/dev/null || fail "compose up failed"

echo "===== [1] wait for backend health ====="
deadline=$((SECONDS + 60))
until curl -sf http://localhost:8081/api/v1/health/live >/dev/null 2>&1; do
  if (( SECONDS > deadline )); then
    fail "backend never became live"
  fi
  sleep 1
done

echo "===== [2] wait for testauth /jwks ====="
until curl -sf http://localhost:8090/jwks >/dev/null 2>&1; do
  if (( SECONDS > deadline )); then
    fail "testauth /jwks never responded"
  fi
  sleep 1
done

echo "===== [3] seed allowlist ====="
docker compose -f "$COMPOSE_FILE" exec -T backend \
  photo-app admin add-user --uid=alice --role=member >/dev/null || fail "add-user alice"
docker compose -f "$COMPOSE_FILE" exec -T backend \
  photo-app admin add-user --uid=root --role=admin >/dev/null || fail "add-user root"

echo "===== [4] initial index ====="
docker compose -f "$COMPOSE_FILE" exec -T backend photo-app index >/dev/null || fail "photo-app index"

MEMBER_TOK="$(curl -sf "http://localhost:8090/mint?sub=alice&email=alice@example.com&verified=1")"
ADMIN_TOK="$(curl -sf "http://localhost:8090/mint?sub=root&email=root@example.com&verified=1")"
EXPIRED_TOK="$(curl -sf "http://localhost:8090/mint?sub=alice&email=alice@example.com&verified=1&exp=-60")"
FOREIGN_TOK="$(curl -sf "http://localhost:8090/mint?sub=nobody&email=nobody@example.com&verified=1")"

echo "===== [5] auth boundary checks ====="
code() { curl -s -o /dev/null -w "%{http_code}" "$@"; }

[[ "$(code http://localhost:8081/api/v1/me)" == 401 ]] || fail "no-token should 401"
[[ "$(code -H "Authorization: Bearer $EXPIRED_TOK" http://localhost:8081/api/v1/me)" == 401 ]] || fail "expired should 401"
[[ "$(code -H "Authorization: Bearer $FOREIGN_TOK" http://localhost:8081/api/v1/photos)" == 403 ]] || fail "foreign should 403"

ME="$(curl -sf -H "Authorization: Bearer $MEMBER_TOK" http://localhost:8081/api/v1/me)"
echo "$ME" | jq -e '.uid == "alice" and .role == "member"' >/dev/null || fail "/me body=$ME"

# member cannot see admin routes
[[ "$(code -H "Authorization: Bearer $MEMBER_TOK" http://localhost:8081/api/v1/admin/users)" == 403 ]] \
  || fail "member should 403 on admin/users"

echo "===== [6] catalog endpoints ====="
curl -sf -H "Authorization: Bearer $MEMBER_TOK" http://localhost:8081/api/v1/categories \
  | jq -e '.items | length == 1' >/dev/null || fail "categories"

PHOTOS_JSON="$(curl -sf -H "Authorization: Bearer $MEMBER_TOK" 'http://localhost:8081/api/v1/photos?limit=10')"
PHOTO_COUNT="$(echo "$PHOTOS_JSON" | jq -r '.items | length')"
[[ "$PHOTO_COUNT" == 3 ]] || fail "expected 3 photos, got $PHOTO_COUNT"

PHOTO_ID="$(echo "$PHOTOS_JSON" | jq -r '.items[0].id')"
PHOTO_KEY="$(echo "$PHOTOS_JSON" | jq -r '.items[0].thumbnail_key')"
[[ -n "$PHOTO_ID" && -n "$PHOTO_KEY" && "$PHOTO_KEY" != null ]] \
  || fail "photo id or thumbnail_key missing: id=$PHOTO_ID key=$PHOTO_KEY"

echo "===== [7] media redirects served by nginx ====="
THUMB_STATUS="$(code -H "Authorization: Bearer $MEMBER_TOK" \
  "http://localhost:8081/api/v1/photos/$PHOTO_ID/thumbnail/$PHOTO_KEY")"
[[ "$THUMB_STATUS" == 200 ]] || fail "thumbnail status=$THUMB_STATUS"

curl -sf -H "Authorization: Bearer $MEMBER_TOK" -o "$TMP_ROOT/thumb.webp" \
  "http://localhost:8081/api/v1/photos/$PHOTO_ID/thumbnail/$PHOTO_KEY" || fail "thumbnail body fetch"
file "$TMP_ROOT/thumb.webp" | grep -qi "Web/P" || fail "thumbnail bytes not WebP ($(file $TMP_ROOT/thumb.webp))"

curl -sf -H "Authorization: Bearer $MEMBER_TOK" -o "$TMP_ROOT/orig.jpg" \
  "http://localhost:8081/api/v1/photos/$PHOTO_ID/original" || fail "original body fetch"
file "$TMP_ROOT/orig.jpg" | grep -qi "JPEG" || fail "original bytes not JPEG"

RANGE_CODE="$(curl -s -o /dev/null -w '%{http_code}' \
  -H "Authorization: Bearer $MEMBER_TOK" \
  -H "Range: bytes=0-99" \
  "http://localhost:8081/api/v1/photos/$PHOTO_ID/original")"
[[ "$RANGE_CODE" == 206 || "$RANGE_CODE" == 200 ]] || fail "range status=$RANGE_CODE"

echo "===== [8] direct internal-media blocked ====="
DIRECT_STATUS="$(code "http://localhost:8081/internal-media/thumbnails/$PHOTO_KEY.webp")"
[[ "$DIRECT_STATUS" == 404 || "$DIRECT_STATUS" == 403 ]] \
  || fail "direct internal-media exposed (status=$DIRECT_STATUS)"

echo "===== [9] key mismatch and traversal ====="
[[ "$(code -H "Authorization: Bearer $MEMBER_TOK" \
    "http://localhost:8081/api/v1/photos/$PHOTO_ID/thumbnail/wrongkey")" == 404 ]] \
  || fail "wrong thumbnail key should 404"
BAD_STATUS="$(code -H "Authorization: Bearer $MEMBER_TOK" \
  'http://localhost:8081/api/v1/photos/1/thumbnail/..%2f..%2fetc%2fpasswd')"
[[ "$BAD_STATUS" == 400 || "$BAD_STATUS" == 404 ]] \
  || fail "traversal should not succeed (status=$BAD_STATUS)"

echo "===== [10] admin index-run async ====="
RUN_JSON="$(curl -sf -X POST -H "Authorization: Bearer $ADMIN_TOK" \
  -H "Content-Type: application/json" -d '{}' \
  http://localhost:8081/api/v1/admin/index-runs)"
SCAN_ID="$(echo "$RUN_JSON" | jq -r '.scan_id')"
[[ -n "$SCAN_ID" && "$SCAN_ID" != null ]] || fail "no scan_id: $RUN_JSON"

for _ in $(seq 1 30); do
  STATUS="$(curl -sf -H "Authorization: Bearer $ADMIN_TOK" \
    "http://localhost:8081/api/v1/admin/index-runs/$SCAN_ID" | jq -r '.status')"
  if [[ "$STATUS" != "running" ]]; then break; fi
  sleep 1
done
[[ "$STATUS" == "succeeded" ]] || fail "scan status=$STATUS"

echo "===== [11] log scrub ====="
if docker compose -f "$COMPOSE_FILE" logs backend --no-color 2>&1 | grep -q 'Bearer '; then
  fail "backend log leaked Bearer token"
fi

echo "ACCEPTANCE-API OK"
