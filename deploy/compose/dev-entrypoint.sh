#!/usr/bin/env sh
# Dev-stack entrypoint: index the fixture library, seed the allowlist, then
# serve. Idempotent — safe to re-run against the persisted data volume.
set -eu

echo "[dev-entrypoint] indexing $PHOTO_ROOT ..."
/usr/local/bin/photo-app index

if [ -f /dev-users/users.json ]; then
  echo "[dev-entrypoint] bootstrapping dev users ..."
  python3 - <<'PY'
import json, subprocess

with open("/dev-users/users.json") as f:
    users = json.load(f)

for u in users:
    args = ["/usr/local/bin/photo-app", "admin", "add-user",
            f"--uid={u['uid']}", f"--email={u['email']}", f"--role={u['role']}"]
    # already-exists is not an error on a re-run.
    subprocess.run(args, check=False)
PY
fi

echo "[dev-entrypoint] serving on $HTTP_LISTEN ..."
exec /usr/local/bin/photo-app serve
