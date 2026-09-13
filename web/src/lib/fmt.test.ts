import { groupByYearMonth } from "./fmt";
import type { Photo } from "../api/types";
import { test, expect } from "vitest";

const p = (id: number, taken?: string): Photo => ({
  id,
  album_id: 1,
  filename: `f${id}.jpg`,
  relative_path: "",
  mime_type: "image/jpeg",
  file_size: 1,
  file_mtime_ns: 1,
  taken_at: taken,
});

test("groups preserving input order within group", () => {
  const groups = groupByYearMonth([
    p(1, "2025-05-04T00:00:00Z"),
    p(2, "2025-05-01T00:00:00Z"),
    p(3, "2025-04-30T00:00:00Z"),
  ]);
  expect(groups.map((g) => g.key)).toEqual(["2025-05", "2025-04"]);
  expect(groups[0].photos.map((x) => x.id)).toEqual([1, 2]);
});

test("skips photos without taken_at", () => {
  const groups = groupByYearMonth([p(1), p(2, "2025-05-01T00:00:00Z")]);
  expect(groups.flatMap((g) => g.photos.map((x) => x.id))).toEqual([2]);
});

test("skips an unparseable taken_at", () => {
  expect(groupByYearMonth([p(1, "not-a-date")])).toEqual([]);
});

test("reads the backend's naive timestamps as local time", () => {
  // The API stores "2025-05-31T23:30:00" with no zone; parsing it as UTC would
  // push it into the next month for anyone east of Greenwich.
  const groups = groupByYearMonth([p(1, "2025-05-31T23:30:00")]);
  expect(groups[0].key).toBe("2025-05");
});

test("a repeated month reuses its existing group", () => {
  const groups = groupByYearMonth([
    p(1, "2025-05-04T00:00:00Z"),
    p(2, "2025-04-30T00:00:00Z"),
    p(3, "2025-05-02T00:00:00Z"),
  ]);
  expect(groups).toHaveLength(2);
  expect(groups[0].photos.map((x) => x.id)).toEqual([1, 3]);
});
