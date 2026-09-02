#!/bin/bash
# End-to-end acceptance for the photo indexer POC.
#
# Runs inside the "acceptance" docker stage which provides libvips-tools,
# exiftool, sqlite3 and the built photo-app binary. The host Makefile mounts
# /fixtures read-only.

set -euo pipefail

fail() { echo "ACCEPTANCE FAIL: $*" >&2; exit 1; }
scan_col() { sqlite3 "$1" "SELECT ${2} FROM scan_runs ORDER BY id DESC LIMIT 1"; }
photo_count() { sqlite3 "$1" 'SELECT COUNT(*) FROM photos'; }
thumb_count() {
  local d="$1"
  # shellcheck disable=SC2012
  find "$d" -maxdepth 1 -type f -name '*.webp' | wc -l | tr -d ' '
}

FIXTURE_DIR=${FIXTURE_DIR:-/fixtures/20170627阿里山二日遊}
SRC_1="$FIXTURE_DIR/LINE_ALBUM_阿里山二日遊_231225_1.jpg"
SRC_2="$FIXTURE_DIR/LINE_ALBUM_阿里山二日遊_231225_2.jpg"
SRC_3="$FIXTURE_DIR/LINE_ALBUM_阿里山二日遊_231225_3.jpg"
for f in "$SRC_1" "$SRC_2" "$SRC_3"; do
  [[ -r "$f" ]] || fail "fixture missing: $f"
done

ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT

PHOTOS="$ROOT/photos"
DATA="$ROOT/data"
THUMBS="$ROOT/thumbnails"
ALBUM="$PHOTOS/旅遊/20170627阿里山二日遊"
mkdir -p "$ALBUM" "$DATA" "$THUMBS"

cp "$SRC_1" "$ALBUM/photo_1.jpg"
cp "$SRC_2" "$ALBUM/photo_2.jpg"
cp "$SRC_3" "$ALBUM/photo_3.jpg"
exiftool -overwrite_original -DateTimeOriginal='2017:06:27 14:03:02' "$ALBUM/photo_1.jpg" >/dev/null

DB="$DATA/photo.db"
export PHOTO_ROOT="$PHOTOS" DATA_DIR="$DATA" THUMBNAIL_DIR="$THUMBS"

echo "===== [1] first index ====="
photo-app index
[[ "$(sqlite3 "$DB" 'SELECT COUNT(*) FROM categories')" == 1 ]] || fail "categories != 1"
[[ "$(sqlite3 "$DB" 'SELECT COUNT(*) FROM albums')"     == 1 ]] || fail "albums != 1"
[[ "$(photo_count "$DB")"                                == 3 ]] || fail "photos != 3"
[[ "$(thumb_count "$THUMBS")"                            == 3 ]] || fail "thumbnails != 3"
[[ "$(scan_col "$DB" files_new)"                         == 3 ]] || fail "scan.new != 3"

TAKEN=$(sqlite3 "$DB" "SELECT taken_at FROM photos WHERE filename='photo_1.jpg'")
[[ "$TAKEN" == "2017-06-27T14:03:02" ]] || fail "taken_at=$TAKEN"

echo "===== [2] second index (no changes) ====="
photo-app index
[[ "$(scan_col "$DB" files_unchanged)" == 3 ]] || fail "unchanged != 3"
[[ "$(scan_col "$DB" files_new)"       == 0 ]] || fail "second scan new != 0"

echo "===== [3] touch photo_2 -> CHANGED ====="
touch -m -d '2020-01-02 12:00:00' "$ALBUM/photo_2.jpg"
photo-app index
[[ "$(scan_col "$DB" files_changed)" == 1 ]] || fail "changed != 1"

echo "===== [4] delete photo_3 -> REMOVED ====="
rm "$ALBUM/photo_3.jpg"
photo-app index
[[ "$(scan_col "$DB" files_removed)" == 1 ]] || fail "removed != 1"
[[ "$(photo_count "$DB")"            == 2 ]] || fail "photos after remove != 2"
[[ "$(thumb_count "$THUMBS")"        == 2 ]] || fail "thumbs after remove != 2"

echo "===== [5] missing root -> failure preserves index ====="
mv "$PHOTOS" "$PHOTOS.bak"
set +e
photo-app index
rc=$?
set -e
[[ $rc -ne 0 ]] || fail "index on missing root should exit non-zero (got $rc)"
[[ "$(photo_count "$DB")" == 2 ]] || fail "photo count changed on failure"
mv "$PHOTOS.bak" "$PHOTOS"

echo "===== [6] rebuild ====="
photo-app rebuild
[[ "$(photo_count "$DB")"     == 2 ]] || fail "rebuild photos != 2"
[[ "$(thumb_count "$THUMBS")" == 2 ]] || fail "rebuild thumbs != 2"

echo "===== [7] orientation applied ====="
ORIENT_ROOT="$ROOT/orient"
ORIENT_ALBUM="$ORIENT_ROOT/misc/orient"
ORIENT_DATA="$ROOT/orient-data"
ORIENT_THUMBS="$ROOT/orient-thumbs"
mkdir -p "$ORIENT_ALBUM" "$ORIENT_DATA" "$ORIENT_THUMBS"
cp "$SRC_1" "$ORIENT_ALBUM/portrait.jpg"
# Baseline landscape? Peek dims; then set Orientation=6 (rotate 90 CW).
SRC_W=$(vipsheader -f width  "$ORIENT_ALBUM/portrait.jpg")
SRC_H=$(vipsheader -f height "$ORIENT_ALBUM/portrait.jpg")
exiftool -overwrite_original -Orientation=6 -n "$ORIENT_ALBUM/portrait.jpg" >/dev/null
PHOTO_ROOT="$ORIENT_ROOT" DATA_DIR="$ORIENT_DATA" THUMBNAIL_DIR="$ORIENT_THUMBS" photo-app index
ORIENT_KEY=$(sqlite3 "$ORIENT_DATA/photo.db" "SELECT thumbnail_key FROM photos WHERE filename='portrait.jpg'")
[[ -n "$ORIENT_KEY" ]] || fail "no thumbnail key for orientation test"
TW=$(vipsheader -f width  "$ORIENT_THUMBS/$ORIENT_KEY.webp")
TH=$(vipsheader -f height "$ORIENT_THUMBS/$ORIENT_KEY.webp")
echo "orient src=${SRC_W}x${SRC_H} -> thumb=${TW}x${TH}"
if [[ $SRC_W -ge $SRC_H ]]; then
  # Landscape rotated 90° should be portrait after thumbnail.
  [[ $TH -gt $TW ]] || fail "orientation not applied (thumb ${TW}x${TH} still landscape)"
else
  [[ $TW -gt $TH ]] || fail "orientation not applied (thumb ${TW}x${TH} still portrait)"
fi

echo "===== [8] no upscale for small images ====="
SMALL_ROOT="$ROOT/small"
SMALL_ALBUM="$SMALL_ROOT/misc/tiny"
SMALL_DATA="$ROOT/small-data"
SMALL_THUMBS="$ROOT/small-thumbs"
mkdir -p "$SMALL_ALBUM" "$SMALL_DATA" "$SMALL_THUMBS"
# Use vips to synthesize a 100x80 RGB PNG.
vips black "$SMALL_ALBUM/tiny.png" 100 80 --bands 3
PHOTO_ROOT="$SMALL_ROOT" DATA_DIR="$SMALL_DATA" THUMBNAIL_DIR="$SMALL_THUMBS" photo-app index
SMALL_KEY=$(sqlite3 "$SMALL_DATA/photo.db" "SELECT thumbnail_key FROM photos WHERE filename='tiny.png'")
[[ -n "$SMALL_KEY" ]] || fail "no thumbnail key for upscale test"
UW=$(vipsheader -f width  "$SMALL_THUMBS/$SMALL_KEY.webp")
UH=$(vipsheader -f height "$SMALL_THUMBS/$SMALL_KEY.webp")
echo "small src=100x80 -> thumb=${UW}x${UH}"
[[ $UW -le 100 && $UH -le 80 ]] || fail "small image was upscaled to ${UW}x${UH}"

echo "===== [9] measurements (Spec 4 must run on target NAS) ====="
echo "photos=$(photo_count "$DB")"
echo "thumbnail_bytes=$(du -sb "$THUMBS" | cut -f1)"
echo "peak_rss=deferred (target NAS DS716+II under Spec 4)"
echo "scan_duration=deferred (target NAS DS716+II under Spec 4)"

echo "ACCEPTANCE OK"
