#!/usr/bin/env bash
# Guards the two ways a tunnel config goes wrong: a placeholder shipped to
# production, or an ingress rule that reaches something it must not.
set -uo pipefail

CFG="${1:-deploy/cloudflared/config.yml}"
MODE="${2:-template}"   # template | deployed
fail=0

# Every ingress service must be the nginx photo service or the closing 404.
while read -r svc; do
  [ -z "$svc" ] && continue
  case "$svc" in
    "http://nginx:8080"|"http_status:404") ;;
    *) echo "FAIL ingress routes to '$svc' — only the nginx photo service is allowed"; fail=1 ;;
  esac
done < <(grep -E '^[[:space:]]+(- )?service:' "$CFG" | sed -E 's/.*service:[[:space:]]*//')

# A catch-all must terminate the list, or an unmatched host falls through.
if ! tail -3 "$CFG" | grep -q 'service: http_status:404'; then
  echo "FAIL config does not end with a http_status:404 catch-all"; fail=1
fi

# Nothing may point at DSM, SMB, SSH or Container Manager.
if grep -qiE ':5000|:5001|:445|:139|:22[^0-9]|synology|diskstation|container-manager' "$CFG"; then
  echo "FAIL config mentions a DSM/SMB/SSH/Container Manager target"; fail=1
fi

# A credential must never be inline.
if grep -qiE 'credentials-json|AccountTag|TunnelSecret' "$CFG"; then
  echo "FAIL credential material is inline in the config"; fail=1
fi

if [ "$MODE" = deployed ] && grep -q 'REPLACE_WITH' "$CFG"; then
  echo "FAIL placeholders are still present in a deployed config"; fail=1
fi

[ "$fail" -eq 0 ] || { echo "verify-tunnel-config FAILED ($CFG, mode=$MODE)"; exit 1; }
echo "verify-tunnel-config OK ($CFG, mode=$MODE)"
