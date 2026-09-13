export type Category = { id: number; name: string; relative_path: string };

export type Album = {
  id: number;
  category_id: number;
  name: string;
  relative_path: string;
  cover_photo_id?: number;
  cover_thumbnail_key?: string;
};

export type Photo = {
  id: number;
  album_id: number;
  filename: string;
  relative_path: string;
  mime_type: string;
  width?: number;
  height?: number;
  taken_at?: string;
  thumbnail_key?: string;
  file_size: number;
  file_mtime_ns: number;
};

/** Cursor-paginated list response. An empty next_cursor means the last page. */
export type Page<T> = { items: T[]; next_cursor?: string };

/** Endpoints that return every row at once (categories, category albums). */
export type List<T> = { items: T[] };

export type Me = { uid: string; email: string | null; role: "admin" | "member" };

/** Backend error shape: {"error": {"code", "message"}} with X-Request-Id header. */
export type ErrorEnvelope = { error?: { code?: string; message?: string }; request_id?: string };
