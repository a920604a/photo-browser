import { useEffect, useRef } from "react";

/**
 * Watches a sentinel element and calls onLoadMore once it comes within 400px
 * of the viewport, so a list keeps filling ahead of the scroll position.
 * Returns the ref to attach; render the sentinel only while hasMore is true.
 */
export function useInfiniteSentinel<T extends HTMLElement>(
  hasMore: boolean,
  onLoadMore: () => void,
) {
  const ref = useRef<T | null>(null);
  useEffect(() => {
    const node = ref.current;
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
  return ref;
}
