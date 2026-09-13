import { useParams } from "react-router-dom";
import { useAlbum, useAlbumPhotos } from "../api/queries";
import { Header } from "../components/layout/Header";
import { Loading } from "../components/states/Loading";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { errorKind } from "../components/states/error-kind";
import { Empty } from "../components/states/Empty";
import { PhotoGrid } from "../components/grid/PhotoGrid";

export function AlbumDetail() {
  const { albumId } = useParams();
  const id = Number(albumId);
  const meta = useAlbum(id);
  const q = useAlbumPhotos(id);
  const photos = q.data?.pages.flatMap((p) => p.items) ?? [];
  const failure = meta.error ?? q.error;
  return (
    <>
      <Header title={meta.data?.name ?? "Album"} />
      {failure ? (
        <ErrorPanel
          kind={errorKind(failure)}
          onRetry={() => {
            void q.refetch();
            void meta.refetch();
          }}
        />
      ) : q.isLoading || meta.isLoading ? (
        <Loading />
      ) : photos.length === 0 ? (
        <Empty title="Album is empty" />
      ) : (
        <PhotoGrid
          photos={photos}
          from={`album-${id}`}
          hasMore={!!q.hasNextPage}
          onLoadMore={() => void q.fetchNextPage()}
        />
      )}
    </>
  );
}
