#!/usr/bin/env bash
# Asserts the production image's non-negotiable properties. Cheap enough to run
# on every build; catches a Dockerfile edit that quietly loosens the target.
set -euo pipefail

IMAGE="${1:?usage: verify-image.sh <image>}"
fail=0
check() { if [ "$2" = "$3" ]; then echo "ok    $1"; else echo "FAIL  $1: got '$2' want '$3'"; fail=1; fi; }

check "architecture is amd64" "$(docker image inspect -f '{{.Architecture}}' "$IMAGE")" "amd64"
check "os is linux"           "$(docker image inspect -f '{{.Os}}' "$IMAGE")"           "linux"
check "runs as non-root"      "$(docker image inspect -f '{{.Config.User}}' "$IMAGE")"  "65532:65532"
check "entrypoint is photo-app" "$(docker image inspect -f '{{index .Config.Entrypoint 0}}' "$IMAGE")" "photo-app"

# GOAMD64 is a build-time setting, so it is not visible on the image. Assert it
# at the source instead — a relaxed value is the failure we actually care about,
# because Braswell has no AVX2 and the binary would crash on the target.
if grep -qE 'GOAMD64=v[234]' Dockerfile; then
  echo "FAIL  Dockerfile raises GOAMD64 above v1 (Braswell has no AVX2)"
  fail=1
else
  echo "ok    Dockerfile keeps GOAMD64=v1"
fi

# The subcommands the prod compose file depends on must exist in this image.
usage="$(docker run --rm "$IMAGE" 2>&1 || true)"
for sub in healthcheck backup serve index; do
  if printf '%s' "$usage" | grep -q "photo-app $sub"; then
    echo "ok    image exposes '$sub'"
  else
    echo "FAIL  image does not expose '$sub'"
    fail=1
  fi
done

# Nothing credential-shaped should be baked into the image.
if docker image inspect -f '{{json .Config.Env}}' "$IMAGE" | grep -qiE 'token|secret|password|credential'; then
  echo "FAIL  image env looks like it carries a credential"
  fail=1
else
  echo "ok    image env carries no credential-shaped variables"
fi

[ "$fail" -eq 0 ] || { echo "verify-image FAILED"; exit 1; }
echo "verify-image OK"
