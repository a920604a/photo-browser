import type { Photo } from "../api/types";

export type Group = { key: string; label: string; photos: Photo[] };

/**
 * Buckets photos into Year/Month sections, keeping the API's ordering both
 * between and within groups. Undated photos are dropped — the timeline is a
 * chronology, and a photo with no taken_at has no place in it.
 */
export function groupByYearMonth(photos: Photo[]): Group[] {
  const out: Group[] = [];
  const idx = new Map<string, number>();
  for (const p of photos) {
    if (!p.taken_at) continue;
    const d = new Date(p.taken_at);
    if (Number.isNaN(d.getTime())) continue;
    const y = d.getFullYear();
    const m = d.getMonth() + 1;
    const key = `${y}-${String(m).padStart(2, "0")}`;
    let i = idx.get(key);
    if (i === undefined) {
      i = out.length;
      idx.set(key, i);
      out.push({ key, label: `${y} / ${String(m).padStart(2, "0")}`, photos: [] });
    }
    out[i].photos.push(p);
  }
  return out;
}
