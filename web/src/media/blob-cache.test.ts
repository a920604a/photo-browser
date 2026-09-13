import { BlobCache } from "./blob-cache";
import { vi, test, expect, beforeEach } from "vitest";
import type { ApiClient } from "../api/client";

function makeClient(...blobs: Blob[]): ApiClient {
  return {
    getJson: vi.fn(),
    getBlob: vi.fn().mockImplementation(() => {
      const b = blobs.shift();
      if (!b) throw new Error("no more blobs");
      return Promise.resolve(b);
    }),
  };
}

const b = (bytes = 1) => new Blob([new Uint8Array(bytes).fill(1)], { type: "image/webp" });

beforeEach(() => {
  (globalThis.URL.createObjectURL as unknown) = vi
    .fn()
    .mockImplementation(() => "blob://" + Math.random());
  (globalThis.URL.revokeObjectURL as unknown) = vi.fn();
});

test("dedupes concurrent fetches", async () => {
  const c = makeClient(b());
  const cache = new BlobCache({ api: c, capacity: 10, concurrency: 4 });
  const [a, b2] = await Promise.all([cache.get("/x"), cache.get("/x")]);
  expect(a.objectUrl).toBe(b2.objectUrl);
  expect(c.getBlob).toHaveBeenCalledTimes(1);
});

test("a second get of a cached path does not refetch", async () => {
  const c = makeClient(b());
  const cache = new BlobCache({ api: c, capacity: 10, concurrency: 4 });
  const first = await cache.get("/x");
  const second = await cache.get("/x");
  expect(second.objectUrl).toBe(first.objectUrl);
  expect(c.getBlob).toHaveBeenCalledTimes(1);
});

test("LRU eviction revokes", async () => {
  const c = makeClient(b(), b(), b());
  const cache = new BlobCache({ api: c, capacity: 2, concurrency: 4 });
  await cache.get("/a");
  await cache.get("/b");
  await cache.get("/c");
  expect(URL.revokeObjectURL).toHaveBeenCalledTimes(1);
});

test("a re-read keeps an entry alive past eviction", async () => {
  const c = makeClient(b(), b(), b());
  const cache = new BlobCache({ api: c, capacity: 2, concurrency: 4 });
  const a = await cache.get("/a");
  await cache.get("/b");
  await cache.get("/a"); // /a is now the most recent, so /b is evicted
  await cache.get("/c");
  expect((await cache.get("/a")).objectUrl).toBe(a.objectUrl);
  expect(c.getBlob).toHaveBeenCalledTimes(3);
});

test("caches error and retries after ttl", async () => {
  const c: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi
      .fn()
      .mockRejectedValueOnce(Object.assign(new Error("nf"), { status: 404 }))
      .mockResolvedValueOnce(b()),
  };
  const cache = new BlobCache({ api: c, capacity: 10, concurrency: 4, errorTtlMs: 10 });
  await expect(cache.get("/x")).rejects.toBeDefined();
  await expect(cache.get("/x")).rejects.toBeDefined();
  expect(c.getBlob).toHaveBeenCalledTimes(1);
  await new Promise((r) => setTimeout(r, 15));
  await cache.get("/x");
  expect(c.getBlob).toHaveBeenCalledTimes(2);
});

test("concurrency limit gates parallel fetches", async () => {
  let inflight = 0;
  let peak = 0;
  const client: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi.fn().mockImplementation(async () => {
      inflight++;
      peak = Math.max(peak, inflight);
      await new Promise((r) => setTimeout(r, 10));
      inflight--;
      return b();
    }),
  };
  const cache = new BlobCache({ api: client, capacity: 100, concurrency: 2 });
  await Promise.all(Array.from({ length: 6 }, (_, i) => cache.get("/p" + i)));
  expect(peak).toBeLessThanOrEqual(2);
});

test("dispose revokes every live object url", async () => {
  const c = makeClient(b(), b());
  const cache = new BlobCache({ api: c, capacity: 10, concurrency: 4 });
  await cache.get("/a");
  await cache.get("/b");
  cache.dispose();
  expect(URL.revokeObjectURL).toHaveBeenCalledTimes(2);
  expect((globalThis.URL.createObjectURL as ReturnType<typeof vi.fn>).mock.calls).toHaveLength(2);
});
