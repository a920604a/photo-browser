import { useParams, useSearchParams, useNavigate } from "react-router-dom";
import { PhotoViewer } from "../components/viewer/PhotoViewer";
import { useAllPhotos, useAlbumPhotos, usePhoto, useTimeline } from "../api/queries";
import { Loading } from "../components/states/Loading";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { errorKind } from "../components/states/error-kind";
import type { Photo } from "../api/types";

export function Viewer() {
  const { photoId } = useParams();
  const [sp] = useSearchParams();
  const from = sp.get("from") ?? "";
  const id = Number(photoId);
  const nav = useNavigate();

  const siblings = useSiblings(from);
  const inSiblings =
    siblings.state === "ready" ? (siblings.photos.find((p) => p.id === id) ?? null) : null;
  // Deep links (and photos past the loaded pages) fall back to fetching the one
  // photo, so the viewer still opens — just without prev/next.
  const single = usePhoto(id, Number.isFinite(id) && siblings.state === "ready" && !inSiblings);

  if (siblings.state === "loading") return <Loading />;
  if (siblings.state === "error") {
    return <ErrorPanel kind={errorKind(siblings.error)} onRetry={() => nav(0)} />;
  }

  const current = inSiblings ?? single.data;
  if (!current) {
    if (single.isLoading) return <Loading />;
    return <ErrorPanel kind={single.error ? errorKind(single.error) : "not-found"} onRetry={() => nav(-1)} />;
  }

  return (
    <PhotoViewer
      current={current}
      siblings={inSiblings ? siblings.photos : [current]}
      from={from}
    />
  );
}

type SibState =
  | { state: "loading" }
  | { state: "error"; error: unknown }
  | { state: "ready"; photos: Photo[] };

/**
 * Resolves ?from= to the collection the grid was showing. All three hooks are
 * called unconditionally (hook rules) but only the matching one is enabled, so
 * the other two issue no request.
 */
function useSiblings(from: string): SibState {
  const albumId = parseAlbumFrom(from);
  const isTimeline = TIMELINE_FROM.test(from);
  const all = useAllPhotos(from === "all");
  const albumQ = useAlbumPhotos(albumId ?? 0, albumId !== null);
  const tlQ = useTimeline(isTimeline);

  if (from === "all") return summarize(all);
  if (albumId !== null) return summarize(albumQ);
  if (isTimeline) return summarize(tlQ);
  return { state: "ready", photos: [] };
}

/** Timeline tiles pass from=t<YYYY-MM>, one value per month section. */
const TIMELINE_FROM = /^t\d{4}-\d{2}$/;

function parseAlbumFrom(from: string): number | null {
  const m = /^album-(\d+)$/.exec(from);
  return m ? Number(m[1]) : null;
}

function summarize(q: ReturnType<typeof useAllPhotos>): SibState {
  if (q.isLoading) return { state: "loading" };
  if (q.error) return { state: "error", error: q.error };
  return { state: "ready", photos: q.data?.pages.flatMap((p) => p.items) ?? [] };
}
