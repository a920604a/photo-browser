import type { ApiClient } from "../api/client";

type Entry = { kind: "ready"; objectUrl: string } | { kind: "error"; error: unknown; ts: number };

type Opts = {
  api: ApiClient;
  capacity?: number;
  concurrency?: number;
  errorTtlMs?: number;
};

/**
 * Fetches protected media through the authenticated API client and hands back
 * object URLs an <img> can use.
 *
 * Three things it has to get right: never fetch the same path twice at once
 * (a grid mounts dozens of tiles in one tick), never let more than a handful of
 * requests run in parallel (mobile links choke), and always revoke an object URL
 * when its entry leaves the cache — otherwise the blobs stay in memory forever.
 * Failures are cached briefly too, so a 404 thumbnail doesn't get retried on
 * every re-render.
 */
export class BlobCache {
  private capacity: number;
  private concurrency: number;
  private errorTtl: number;
  private lru = new Map<string, Entry>(); // insertion order = LRU
  private inflight = new Map<string, Promise<Entry>>();
  private queue: Array<() => void> = [];
  private active = 0;
  private api: ApiClient;

  constructor(o: Opts) {
    this.api = o.api;
    this.capacity = o.capacity ?? 50;
    this.concurrency = o.concurrency ?? 4;
    this.errorTtl = o.errorTtlMs ?? 30_000;
  }

  async get(path: string): Promise<{ objectUrl: string }> {
    const cached = this.lru.get(path);
    if (cached) {
      if (cached.kind === "ready") {
        this.lru.delete(path);
        this.lru.set(path, cached);
        return { objectUrl: cached.objectUrl };
      }
      if (Date.now() - cached.ts < this.errorTtl) throw cached.error;
      this.lru.delete(path);
    }
    const inflight = this.inflight.get(path);
    if (inflight) {
      const e = await inflight;
      if (e.kind === "ready") return { objectUrl: e.objectUrl };
      throw e.error;
    }
    const p = this.enqueue(path);
    this.inflight.set(path, p);
    try {
      const e = await p;
      if (e.kind === "ready") return { objectUrl: e.objectUrl };
      throw e.error;
    } finally {
      this.inflight.delete(path);
    }
  }

  /** Revokes every live object URL. Call when the owning tree unmounts. */
  dispose() {
    for (const e of this.lru.values()) if (e.kind === "ready") URL.revokeObjectURL(e.objectUrl);
    this.lru.clear();
  }

  private enqueue(path: string): Promise<Entry> {
    return new Promise((resolve) => {
      const run = async () => {
        this.active++;
        try {
          const blob = await this.api.getBlob(path);
          const entry: Entry = { kind: "ready", objectUrl: URL.createObjectURL(blob) };
          this.insert(path, entry);
          resolve(entry);
        } catch (error) {
          const entry: Entry = { kind: "error", error, ts: Date.now() };
          this.insert(path, entry);
          resolve(entry);
        } finally {
          this.active--;
          const next = this.queue.shift();
          if (next) next();
        }
      };
      if (this.active < this.concurrency) void run();
      else this.queue.push(() => void run());
    });
  }

  private insert(path: string, entry: Entry) {
    this.lru.set(path, entry);
    while (this.lru.size > this.capacity) {
      const oldestKey = this.lru.keys().next().value as string | undefined;
      if (oldestKey === undefined) break;
      const old = this.lru.get(oldestKey);
      this.lru.delete(oldestKey);
      if (old?.kind === "ready") URL.revokeObjectURL(old.objectUrl);
    }
  }
}
