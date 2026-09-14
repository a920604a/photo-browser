#!/usr/bin/env bash
# Samples container CPU and memory while idle and while indexing, and reports
# the peaks. On a 2 GB machine the peak RSS during a scan is the number that
# decides the memory limits — it must leave DSM room to breathe.
set -euo pipefail

COMPOSE="deploy/compose/docker-compose.prodcheck.yml"
OUT="docs/deploy/resource-measurements.md"
IDLE=60
while [ $# -gt 0 ]; do
  case "$1" in
    --compose) COMPOSE="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --idle-seconds) IDLE="$2"; shift 2 ;;
    *) echo "unknown flag: $1" >&2; exit 2 ;;
  esac
done

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# docker stats with no arguments reports every container on the host, which on
# a developer machine means unrelated projects land in the report. Filter by the
# compose project label rather than `compose ps -q`: the latter lists only the
# declared services, and the container that actually does the indexing is a
# one-shot `compose run` container — precisely the one whose peak RSS matters.
PROJECT="$(docker compose -f "$COMPOSE" ps --format '{{.Labels}}' 2>/dev/null \
  | head -1 | tr ',' '\n' | awk -F= '/com.docker.compose.project=/{print $2; exit}')"

project_ids() {
  [ -n "$PROJECT" ] || return 0
  docker ps -q --filter "label=com.docker.compose.project=$PROJECT" 2>/dev/null | tr '\n' ' '
}

sample_once() {
  local ids
  ids="$(project_ids)"
  [ -n "${ids// /}" ] || return 0
  # shellcheck disable=SC2086
  docker stats --no-stream --format '{{.Name}}	{{.CPUPerc}}	{{.MemUsage}}' $ids 2>/dev/null || true
}

summarize() {
  python3 - "$1" "$2" <<'PY'
import re, sys
from collections import defaultdict

path, label = sys.argv[1], sys.argv[2]
cpu, mem = defaultdict(list), defaultdict(list)

def to_mib(text):
    m = re.match(r'([\d.]+)\s*([KMG]i?B)', text.strip(), re.I)
    if not m:
        return 0.0
    v, unit = float(m.group(1)), m.group(2).upper().rstrip('B').rstrip('I')
    return {'K': v / 1024, 'M': v, 'G': v * 1024}.get(unit, v)

for line in open(path):
    parts = line.rstrip('\n').split('\t')
    if len(parts) != 3:
        continue
    name, c, m = parts
    cpu[name].append(float(c.rstrip('%') or 0))
    mem[name].append(to_mib(m.split('/')[0]))

print(f'### {label}')
print()
if not cpu:
    print('No samples collected.')
    print()
    raise SystemExit(0)
print('| service | samples | mean CPU% | peak CPU% | peak RSS (MiB) |')
print('|---|---|---|---|---|')
for name in sorted(cpu):
    c, m = cpu[name], mem[name]
    print(f'| `{name}` | {len(c)} | {sum(c)/len(c):.1f} | {max(c):.1f} | {max(m):.0f} |')
print()
total_peak = sum(max(v) for v in mem.values())
print(f'Summed peak RSS across services: **{total_peak:.0f} MiB** '
      f'(the NAS has 2048 MiB total; DSM itself needs several hundred).')
print()
PY
}

echo "[measure] idle sampling for ${IDLE}s"
: > "$TMP/idle.tsv"
end=$((SECONDS + IDLE))
while [ "$SECONDS" -lt "$end" ]; do
  sample_once >> "$TMP/idle.tsv"
done

# --rebuild-thumbnails is the heaviest operation: it decodes every original,
# which is exactly the peak the memory limit has to accommodate.
echo "[measure] sampling during an index run (rebuilding thumbnails)"
: > "$TMP/scan.tsv"
docker compose -f "$COMPOSE" run --rm --no-deps photo-app index --rebuild-thumbnails >"$TMP/index.log" 2>&1 &
INDEX_PID=$!
while kill -0 "$INDEX_PID" 2>/dev/null; do
  sample_once >> "$TMP/scan.tsv"
done
wait "$INDEX_PID" || true

mkdir -p "$(dirname "$OUT")"
{
  echo "# Resource measurements"
  echo
  echo "Host: \`$(hostname)\`  "
  echo "Compose file: \`$COMPOSE\`  "
  echo "Generated: $(date -Iseconds)"
  echo
  echo '> Numbers taken anywhere other than the DS716+II are indicative only.'
  echo '> The acceptance record requires a run on the NAS itself.'
  echo
  summarize "$TMP/idle.tsv" "Idle"
  summarize "$TMP/scan.tsv" "During \`photo-app index --rebuild-thumbnails\`"
  echo "### Index run output"
  echo
  echo '```'
  grep -v APP_VERSION "$TMP/index.log" | tail -5
  echo '```'
} > "$OUT"

echo "[measure] wrote $OUT"
