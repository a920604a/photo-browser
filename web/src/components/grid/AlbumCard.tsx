import { Link } from "react-router-dom";
import type { Album } from "../../api/types";
import { useAuthedImage, mediaPaths } from "../../media/hooks";

export function AlbumCard({ album }: { album: Album }) {
  const src =
    album.cover_photo_id && album.cover_thumbnail_key
      ? mediaPaths.thumbnail(album.cover_photo_id, album.cover_thumbnail_key)
      : null;
  const img = useAuthedImage(src);
  return (
    <Link
      to={`/albums/${album.id}`}
      className="block overflow-hidden rounded border bg-white shadow-sm dark:border-gray-800 dark:bg-gray-900"
    >
      <div className="aspect-[4/3] bg-gray-200 dark:bg-gray-800">
        {img.state === "ready" ? (
          <img
            src={img.objectUrl}
            alt={album.name}
            loading="lazy"
            className="h-full w-full object-cover"
          />
        ) : img.state === "error" || !src ? (
          <span className="flex h-full w-full items-center justify-center text-xs text-gray-400">
            no cover
          </span>
        ) : (
          <span
            className="block h-full w-full animate-pulse bg-gray-300 dark:bg-gray-700"
            aria-hidden="true"
          />
        )}
      </div>
      <div className="p-3">
        <p className="text-sm font-medium">{album.name}</p>
        <p className="text-xs text-gray-500">/{album.relative_path}</p>
      </div>
    </Link>
  );
}
