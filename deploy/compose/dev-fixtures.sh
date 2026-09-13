#!/usr/bin/env bash
# Materialises a dev photo library from the repo's flat test-photos/ fixtures.
#
# The indexer expects PHOTO_ROOT/<category>/<album>/<photo>, while test-photos/
# is a single flat album, so the dev stack needs a generated two-level tree.
# Output lives in .dev-fixtures/photos (gitignored) and is rebuilt in place.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SRC="$ROOT/test-photos/20170627阿里山二日遊"
OUT="${1:-$ROOT/.dev-fixtures/photos}"

[[ -d "$SRC" ]] || { echo "fixture source missing: $SRC" >&2; exit 1; }

# bash 3.2 (macOS) has no mapfile.
SRCS=()
while IFS= read -r f; do SRCS+=("$f"); done < <(find "$SRC" -type f -name '*.jpg' | sort)
(( ${#SRCS[@]} >= 15 )) || { echo "need >=15 source photos, found ${#SRCS[@]}" >&2; exit 1; }

rm -rf "$OUT"

# album|photo count — spread over two categories so the Categories route has
# something to show, with one album big enough to page through.
i=0
add_album() {
  local album="$1" count="$2" n
  mkdir -p "$OUT/$album"
  for (( n = 0; n < count; n++ )); do
    cp "${SRCS[$(( i % ${#SRCS[@]} ))]}" "$OUT/$album/photo_$(printf '%02d' "$n").jpg"
    i=$(( i + 1 ))
  done
}

add_album "travel/alishan-2017" 12
add_album "travel/japan-2024" 6
add_album "family/birthday-2023" 4

echo "dev fixtures ready at $OUT ($(find "$OUT" -type f | wc -l | tr -d ' ') photos)"
