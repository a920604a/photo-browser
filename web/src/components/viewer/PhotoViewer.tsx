import { useEffect } from "react";
import { Link, useNavigate } from "react-router-dom";
import type { Photo } from "../../api/types";
import { useAuthedImage, mediaPaths } from "../../media/hooks";

type Props = {
  current: Photo;
  /** The already-loaded photos of the collection the viewer was opened from. */
  siblings: Photo[];
  from: string;
};

export function PhotoViewer({ current, siblings, from }: Props) {
  const nav = useNavigate();
  const idx = siblings.findIndex((p) => p.id === current.id);
  const prev = idx > 0 ? siblings[idx - 1] : null;
  const next = idx >= 0 && idx < siblings.length - 1 ? siblings[idx + 1] : null;
  const img = useAuthedImage(mediaPaths.original(current.id));

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.key === "Escape") nav(-1);
      else if (e.key === "ArrowLeft" && prev) nav(`/viewer/${prev.id}?from=${from}`);
      else if (e.key === "ArrowRight" && next) nav(`/viewer/${next.id}?from=${from}`);
    };
    window.addEventListener("keydown", on);
    return () => window.removeEventListener("keydown", on);
  }, [prev, next, nav, from]);

  return (
    <div
      role="dialog"
      aria-modal="true"
      aria-label={current.filename}
      className="fixed inset-0 z-20 flex flex-col bg-black text-white"
    >
      <div className="flex items-center justify-between p-3">
        <button
          onClick={() => nav(-1)}
          className="min-h-touch min-w-touch rounded px-3 py-2"
          aria-label="Close"
        >
          ✕
        </button>
        <div className="text-sm">
          <div className="font-medium">{current.filename}</div>
          {current.taken_at && <div className="text-xs text-gray-300">{current.taken_at}</div>}
        </div>
        <div className="w-11" aria-hidden="true" />
      </div>
      <div className="relative flex flex-1 items-center justify-center">
        {img.state === "ready" ? (
          <img
            src={img.objectUrl}
            alt={current.filename}
            className="max-h-full max-w-full object-contain"
          />
        ) : img.state === "error" ? (
          <p>Photo unavailable</p>
        ) : (
          <p role="status">Loading…</p>
        )}
        {prev && (
          <Link
            to={`/viewer/${prev.id}?from=${from}`}
            aria-label="Previous"
            className="absolute left-2 top-1/2 min-h-touch min-w-touch -translate-y-1/2 rounded bg-white/10 px-3 py-2"
          >
            ‹
          </Link>
        )}
        {next && (
          <Link
            to={`/viewer/${next.id}?from=${from}`}
            aria-label="Next"
            className="absolute right-2 top-1/2 min-h-touch min-w-touch -translate-y-1/2 rounded bg-white/10 px-3 py-2"
          >
            ›
          </Link>
        )}
      </div>
    </div>
  );
}
