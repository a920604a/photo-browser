#!/usr/bin/env bash
# Records everything spec 4 section 3 requires before any image is built.
#
# Safe to run repeatedly; it only reads. Run it ON THE NAS over SSH — the
# numbers that matter (CPU flags, free space, libvips build) are the NAS's,
# not your laptop's.
set -uo pipefail

PHOTO_ROOT="/volume1/photos"
DATA_DIR="/volume1/docker/photo-browser/data"
THUMB_DIR="/volume1/docker/photo-browser/thumbnails"
OUT="preflight-report.md"
IMAGE="${PREFLIGHT_IMAGE:-photo-browser-test:latest}"

while [ $# -gt 0 ]; do
  case "$1" in
    --photo-root) PHOTO_ROOT="$2"; shift 2 ;;
    --data-dir)   DATA_DIR="$2";   shift 2 ;;
    --thumb-dir)  THUMB_DIR="$2";  shift 2 ;;
    --out)        OUT="$2";        shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

blocking=0
say() { printf '%s\n' "$*" >> "$OUT"; }
note_block() { blocking=1; say "- **BLOCKING:** $*"; }

: > "$OUT"
say "# NAS preflight report"
say ""
say "Generated: $(date -Iseconds) on \`$(hostname)\`"
say ""

say "## Platform"
say ""
say '```'
say "uname: $(uname -a)"
say "DSM:   $(cat /etc/VERSION 2>/dev/null | tr '\n' ' ' || echo 'not a DSM host')"
say "CPU:   $(grep -m1 'model name' /proc/cpuinfo 2>/dev/null | cut -d: -f2- | sed 's/^ //')"
say "cores: $(nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo '?')"
say "mem:   $(free -m 2>/dev/null | awk '/^Mem:/{print $2" MB total, "$7" MB available"}')"
say '```'
say ""

# Braswell has no AVX2. Recording the flags proves why GOAMD64 stays at v1.
if grep -qw avx2 /proc/cpuinfo 2>/dev/null; then
  say "- CPU reports avx2 (unexpected for Braswell) — keep \`GOAMD64=v1\` anyway."
else
  say "- CPU has no avx2 (or /proc/cpuinfo is unavailable). \`GOAMD64=v1\` is required."
fi
say ""

say "## Container runtime"
say ""
say '```'
say "docker:  $(docker --version 2>&1 | head -1)"
say "compose: $(docker compose version 2>&1 | head -1)"
say '```'
if ! docker compose version >/dev/null 2>&1; then
  note_block "\`docker compose\` (v2) is unavailable; the prod topology needs it."
fi
say ""

say "## Volumes"
say ""
say "| path | exists | writable | free |"
say "|---|---|---|---|"
for spec_line in "photos:$PHOTO_ROOT:ro" "data:$DATA_DIR:rw" "thumbnails:$THUMB_DIR:rw"; do
  name="${spec_line%%:*}"; rest="${spec_line#*:}"
  path="${rest%:*}"; mode="${rest##*:}"
  exists=no; writable=n/a; free="?"
  [ -d "$path" ] && exists=yes
  if [ "$exists" = yes ]; then
    free="$(df -h "$path" 2>/dev/null | awk 'NR==2{print $4}')"
    if [ "$mode" = rw ]; then
      if [ -w "$path" ]; then writable=yes; else writable=no; fi
    fi
  fi
  say "| \`$path\` ($name, $mode) | $exists | $writable | $free |"
  if [ "$exists" = no ]; then note_block "$path does not exist."; fi
  if [ "$mode" = rw ] && [ "$writable" = no ]; then note_block "$path is not writable."; fi
done
say ""

# Thumbnails plus SQLite must not fill the volume. 5 GiB is a deliberately low
# floor: a 22k-photo library of 512px WebP thumbnails lands near 1 GB.
free_kb="$(df -Pk "$THUMB_DIR" 2>/dev/null | awk 'NR==2{print $4}')"
if [ -n "${free_kb:-}" ] && [ "$free_kb" -lt 5242880 ]; then
  note_block "thumbnail volume has less than 5 GiB free (${free_kb} KiB)."
fi

say "## Service identity"
say ""
say '```'
say "id: $(id)"
say '```'
say "Record the UID/GID that has read-only access to the photo share; the compose file pins it."
say ""

say "## libvips codecs"
say ""
say "Probed inside \`$IMAGE\` (the same libvips build the app uses)."
say ""
say '```'
docker run --rm --entrypoint sh "$IMAGE" -c '
  vips --version
  for f in jpegload pngload webpload webpsave; do
    if vips -l 2>/dev/null | grep -q "$f"; then echo "$f: yes"; else echo "$f: NO"; fi
  done
' 2>&1 | while IFS= read -r line; do say "$line"; done
say '```'
say ""

say "## HEIC probe (must stay unsupported unless this passes)"
say ""
heic_out="$(docker run --rm --entrypoint sh "$IMAGE" -c 'vips -l 2>/dev/null | grep -c heifload' 2>&1 | tr -d '[:space:]')"
say '```'
say "heifload operations found: $heic_out"
say '```'
if [ "$heic_out" = "0" ]; then
  say "- HEIC is **unsupported**, as specified. Do not add HEIC files to the library."
else
  say "- libvips exposes heifload, but HEIC stays **out of POC scope** until a separate"
  say "  memory probe on this 2 GB machine proves a full-size HEIC decode fits."
fi
say ""

say "## EXIF orientation"
say ""
say '```'
docker run --rm --entrypoint sh "$IMAGE" -c 'exiftool -ver' 2>&1 | while IFS= read -r line; do say "exiftool: $line"; done
say '```'
say ""

say "## Time sync (Firebase token verification depends on it)"
say ""
say '```'
say "date: $(date -Iseconds)"
say "ntp:  $(timedatectl 2>/dev/null | tr '\n' ' ' || echo 'check DSM > Control Panel > Regional Options > Time')"
say '```'
say ""

say "## Photo count"
say ""
say "Not collected here. The first read-only \`photo-app index\` records it in"
say "\`scan_runs.files_seen\`; copy that number into this report afterwards."
say ""

if [ "$blocking" -ne 0 ]; then
  echo "preflight FAILED — see $OUT" >&2
  exit 1
fi
echo "preflight OK — wrote $OUT"
