#!/usr/bin/env bash
# Spec 4 section 10 checks, runnable against either the local prodcheck stack
# or the real deployment. Everything here is read-only except the deliberate
# disable/enable of a member, which is restored before the script exits.
set -uo pipefail

BASE_URL=""; HOST=""; MEMBER=""; ADMIN=""; DENIED=""; EDGE=0; COMPOSE=""
while [ $# -gt 0 ]; do
  case "$1" in
    --base-url) BASE_URL="$2"; shift 2 ;;
    --host) HOST="$2"; shift 2 ;;
    --member-token) MEMBER="$2"; shift 2 ;;
    --admin-token) ADMIN="$2"; shift 2 ;;
    --denied-token) DENIED="$2"; shift 2 ;;
    --compose) COMPOSE="$2"; shift 2 ;;   # enables the disable/enable check
    --edge) EDGE=1; shift ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done
[ -n "$BASE_URL" ] || { echo "--base-url is required" >&2; exit 2; }
[ -n "$MEMBER" ] || { echo "--member-token is required" >&2; exit 2; }

fail=0
hdr=(); [ -n "$HOST" ] && hdr=(-H "Host: $HOST")
ok()  { echo "ok    $1"; }
bad() { echo "FAIL  $1"; fail=1; }
code() { curl -s -o /dev/null -w '%{http_code}' "${hdr[@]}" "$@"; }
body() { curl -s "${hdr[@]}" "$@"; }
head_of() { curl -s -D- -o /dev/null "${hdr[@]}" "$@"; }
header_value() { tr -d '\r' | awk -v k="$1" 'BEGIN{IGNORECASE=1} tolower($1)==tolower(k)":" {sub($1" ","");print}'; }

echo "== functional path =="

c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me")
[ "$c" = 200 ] && ok "/me authorizes an allowlisted member" || bad "/me returned $c for a member"

c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/timeline?limit=5")
[ "$c" = 200 ] && ok "timeline is listable" || bad "timeline returned $c"

read -r PID KEY <<<"$(body -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos?limit=1" \
  | python3 -c "import sys,json
try:
    d=json.load(sys.stdin).get('items') or []
except Exception:
    d=[]
print(d[0]['id'], d[0].get('thumbnail_key','')) if d else print('','')" 2>/dev/null)"

if [ -z "${PID:-}" ]; then
  bad "library is empty — index before verifying"
else
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/thumbnail/$KEY")
  [ "$c" = 200 ] && ok "authenticated thumbnail is served" || bad "thumbnail returned $c"
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/original")
  [ "$c" = 200 ] && ok "authenticated original is served" || bad "original returned $c"
fi

echo "== security =="

# Direct internal-media access must never work: nginx marks it `internal`, so
# only an X-Accel-Redirect subrequest can reach it.
for path in "/internal-media/originals/" "/internal-media/thumbnails/" \
            "/internal-media/originals/travel/alishan-2017/photo_00.jpg"; do
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL$path")
  [ "$c" = 404 ] && ok "direct $path is refused ($c)" || bad "direct $path returned $c, want 404"
done

# A valid identity that is not on the allowlist gets 403 everywhere.
if [ -n "$DENIED" ]; then
  for path in "/api/v1/me" "/api/v1/photos" "/api/v1/photos/${PID:-1}/original"; do
    c=$(code -H "Authorization: Bearer $DENIED" "$BASE_URL$path")
    [ "$c" = 403 ] && ok "unallowlisted identity gets 403 on $path" || bad "unallowlisted got $c on $path"
  done
fi

c=$(code "$BASE_URL/api/v1/me")
[ "$c" = 401 ] && ok "no token gets 401" || bad "no token got $c"
c=$(code -H "Authorization: Bearer not.a.token" "$BASE_URL/api/v1/me")
[ "$c" = 401 ] && ok "malformed token gets 401" || bad "malformed token got $c"
c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/admin/users")
[ "$c" = 403 ] && ok "member cannot reach admin routes" || bad "member got $c on admin route"

# Path traversal, in the encodings a proxy might normalise differently.
for t in "../../etc/passwd" "..%2f..%2fetc%2fpasswd" "%2e%2e/%2e%2e/etc/passwd"; do
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/${PID:-1}/thumbnail/$t")
  case "$c" in
    200) bad "traversal '$t' returned 200" ;;
    *) ok "traversal '$t' refused ($c)" ;;
  esac
done

# A thumbnail key that no longer matches the row must not serve bytes.
c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/${PID:-1}/thumbnail/stale-key-that-never-existed")
[ "$c" = 404 ] && ok "stale thumbnail key refused ($c)" || bad "stale thumbnail key returned $c"

# Per-user JSON must be no-store; media must be private, never public.
cc=$(head_of -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me" | header_value cache-control)
[ "$cc" = "no-store" ] && ok "api json is no-store" || bad "api json Cache-Control is '$cc', want no-store"
if [ -n "${PID:-}" ]; then
  cc=$(head_of -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos/$PID/thumbnail/$KEY" | header_value cache-control)
  case "$cc" in
    *private*) ok "thumbnail cache-control is private ($cc)" ;;
    *) bad "thumbnail Cache-Control is '$cc', want private" ;;
  esac
fi

# No URL may carry a token: they travel in the Authorization header only.
if body -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/photos?limit=5" | grep -qiE '(token|jwt|bearer)=' ; then
  bad "a listing response contains a token-bearing URL"
else
  ok "no token-bearing URLs in listing responses"
fi

# A disabled member must lose access on the very next request.
if [ -n "$COMPOSE" ]; then
  docker compose -f "$COMPOSE" run --rm --no-deps photo-app admin disable --uid=member-1 >/dev/null 2>&1
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me")
  [ "$c" = 403 ] && ok "disabled member loses access immediately" || bad "disabled member still got $c"
  docker compose -f "$COMPOSE" run --rm --no-deps photo-app admin enable --uid=member-1 >/dev/null 2>&1
  c=$(code -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me")
  [ "$c" = 200 ] && ok "re-enabled member regains access" || bad "re-enabled member got $c"
fi

if [ "$EDGE" -eq 1 ]; then
  echo "== edge (real Cloudflare only) =="

  cs=$(head_of -H "Authorization: Bearer $MEMBER" "$BASE_URL/api/v1/me" | header_value cf-cache-status)
  case "${cs:-none}" in
    HIT|STALE|REVALIDATED) bad "api response came from Cloudflare's shared cache ($cs)" ;;
    *) ok "api response is not shared-cached (cf-cache-status=${cs:-absent})" ;;
  esac

  for path in "/webman/index.cgi" "/webapi/entry.cgi" "/webman/3rdparty/" "/"; do
    if body "$BASE_URL$path" | head -c 400 | grep -qiE 'synology|diskstation|container manager'; then
      bad "$path exposes DSM content"
    else
      ok "$path exposes no DSM content"
    fi
  done
fi

[ "$fail" -eq 0 ] || { echo; echo "verify-deployment FAILED"; exit 1; }
echo; echo "verify-deployment OK"
