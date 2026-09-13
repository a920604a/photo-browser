import { useEffect, useRef } from "react";
import type { Photo } from "../../api/types";
import { PhotoTile } from "./PhotoTile";

type Props = {
  photos: Photo[];
  from: string;
  hasMore: boolean;
  onLoadMore: () => void;
};

/**
 * A sentinel below the grid drives pagination: once it comes within 400px of
 * the viewport the next cursor page is fetched, so scrolling never stalls at
 * the bottom edge.
 */
export function PhotoGrid({ photos, from, hasMore, onLoadMore }: Props) {
  const sentinel = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    const node = sentinel.current;
    if (!hasMore || !node || typeof IntersectionObserver === "undefined") return;
    const io = new IntersectionObserver(
      (entries) => {
        if (entries.some((e) => e.isIntersecting)) onLoadMore();
      },
      { rootMargin: "400px" },
    );
    io.observe(node);
    return () => io.disconnect();
  }, [hasMore, onLoadMore]);
  return (
    <div className="p-2">
      <div className="grid grid-cols-3 gap-1 sm:grid-cols-4 md:grid-cols-6 lg:grid-cols-8">
        {photos.map((p) => (
          <PhotoTile key={p.id} photo={p} from={from} />
        ))}
      </div>
      {hasMore && <div ref={sentinel} className="h-8" data-testid="grid-sentinel" />}
    </div>
  );
}
