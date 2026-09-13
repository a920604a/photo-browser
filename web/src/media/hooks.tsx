import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import { BlobCache } from "./blob-cache";
import type { ApiClient } from "../api/client";

const MediaCtx = createContext<BlobCache | null>(null);

/** Owns the one BlobCache for the tree, so tiles share fetches and eviction. */
export function MediaProvider({ api, children }: { api: ApiClient; children: ReactNode }) {
  const [cache] = useState(() => new BlobCache({ api }));
  useEffect(() => () => cache.dispose(), [cache]);
  return <MediaCtx.Provider value={cache}>{children}</MediaCtx.Provider>;
}

export function useMedia(): BlobCache {
  const c = useContext(MediaCtx);
  if (!c) throw new Error("useMedia must be inside MediaProvider");
  return c;
}

export type ImageState =
  | { state: "loading" }
  | { state: "ready"; objectUrl: string }
  | { state: "error"; kind: "not-found" | "forbidden" | "network" };

/**
 * Resolves an API media path to an object URL. Pass null to stay idle (e.g. an
 * album with no cover), which reports "loading" rather than firing a request.
 */
export function useAuthedImage(path: string | null): ImageState {
  const cache = useMedia();
  const [s, setS] = useState<ImageState>({ state: "loading" });
  useEffect(() => {
    let alive = true;
    if (!path) {
      setS({ state: "loading" });
      return;
    }
    setS({ state: "loading" });
    cache.get(path).then(
      (r) => {
        if (alive) setS({ state: "ready", objectUrl: r.objectUrl });
      },
      (err: unknown) => {
        if (!alive) return;
        const status = (err as { status?: number } | null)?.status;
        if (status === 404) setS({ state: "error", kind: "not-found" });
        else if (status === 403) setS({ state: "error", kind: "forbidden" });
        else setS({ state: "error", kind: "network" });
      },
    );
    return () => {
      alive = false;
    };
  }, [path, cache]);
  return s;
}

/** Media URLs the API exposes. Kept here so no component hand-builds a path. */
export const mediaPaths = {
  thumbnail: (photoId: number, key: string) => `/photos/${photoId}/thumbnail/${key}`,
  original: (photoId: number) => `/photos/${photoId}/original`,
};
