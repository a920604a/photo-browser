import { Link } from "react-router-dom";
import type { Photo } from "../../api/types";
import { useAuthedImage, mediaPaths } from "../../media/hooks";

export function PhotoTile({ photo, from }: { photo: Photo; from: string }) {
  const src = photo.thumbnail_key ? mediaPaths.thumbnail(photo.id, photo.thumbnail_key) : null;
  const img = useAuthedImage(src);
  return (
    <Link
      to={`/viewer/${photo.id}?from=${from}`}
      className="relative block aspect-square overflow-hidden rounded bg-gray-200 dark:bg-gray-800"
    >
      {img.state === "ready" ? (
        <img
          src={img.objectUrl}
          alt={photo.filename}
          loading="lazy"
          className="h-full w-full object-cover"
        />
      ) : img.state === "error" || !src ? (
        <span className="flex h-full w-full items-center justify-center text-xs text-gray-400">
          n/a
        </span>
      ) : (
        <span
          className="block h-full w-full animate-pulse bg-gray-300 dark:bg-gray-700"
          aria-hidden="true"
        />
      )}
    </Link>
  );
}
