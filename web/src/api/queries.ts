import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { useApi } from "./context";
import type { ApiClient } from "./client";
import type { Album, Category, List, Me, Page, Photo } from "./types";

const withCursor = (base: string, cursor?: string) =>
  cursor ? `${base}${base.includes("?") ? "&" : "?"}cursor=${encodeURIComponent(cursor)}` : base;

/** The backend sends "" on the last page; TanStack wants undefined. */
const nextPage = (last: Page<unknown>) => last.next_cursor || undefined;

/** Shared wiring for the cursor-paginated list endpoints. */
function usePagedQuery<T>(
  api: ApiClient,
  key: readonly unknown[],
  path: string,
  enabled = true,
) {
  return useInfiniteQuery({
    queryKey: key,
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) => api.getJson<Page<T>>(withCursor(path, pageParam)),
    getNextPageParam: nextPage,
    enabled,
  });
}

export function useMe() {
  const api = useApi();
  return useQuery({ queryKey: ["me"], queryFn: () => api.getJson<Me>("/me") });
}

/** /categories returns every row at once — no cursor. */
export function useCategories() {
  const api = useApi();
  return useQuery({
    queryKey: ["categories"],
    queryFn: () => api.getJson<List<Category>>("/categories"),
  });
}

/** /categories/{id}/albums is likewise unpaginated. */
export function useCategoryAlbums(categoryId: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["albums", { categoryId }],
    queryFn: () => api.getJson<List<Album>>(`/categories/${categoryId}/albums`),
  });
}

export function useAlbums() {
  return usePagedQuery<Album>(useApi(), ["albums", "all"], "/albums");
}

export function usePhoto(photoId: number, enabled = true) {
  const api = useApi();
  return useQuery({
    queryKey: ["photo", photoId],
    queryFn: () => api.getJson<Photo>(`/photos/${photoId}`),
    enabled,
  });
}

export function useAlbum(albumId: number) {
  const api = useApi();
  return useQuery({
    queryKey: ["album", albumId],
    queryFn: () => api.getJson<Album>(`/albums/${albumId}`),
  });
}

export function useAlbumPhotos(albumId: number, enabled = true) {
  return usePagedQuery<Photo>(
    useApi(),
    ["photos", { albumId }],
    `/albums/${albumId}/photos`,
    enabled,
  );
}

export function useAllPhotos(enabled = true) {
  return usePagedQuery<Photo>(useApi(), ["photos", "all"], "/photos", enabled);
}

export function useTimeline(enabled = true) {
  return usePagedQuery<Photo>(useApi(), ["photos", "timeline"], "/photos/timeline", enabled);
}
