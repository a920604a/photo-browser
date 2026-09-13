import { Header } from "../components/layout/Header";
import { AlbumCard } from "../components/grid/AlbumCard";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { errorKind } from "../components/states/error-kind";
import { useInfiniteSentinel } from "../hooks/useInfiniteSentinel";
import { useAlbums } from "../api/queries";

export function Albums() {
  const q = useAlbums();
  const albums = q.data?.pages.flatMap((p) => p.items) ?? [];
  const sentinel = useInfiniteSentinel<HTMLDivElement>(!!q.hasNextPage, () => void q.fetchNextPage());
  return (
    <>
      <Header title="Albums" />
      {q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <ErrorPanel kind={errorKind(q.error)} onRetry={() => void q.refetch()} />
      ) : albums.length === 0 ? (
        <Empty title="No albums yet" hint="Albums come from the folders under your photo root." />
      ) : (
        <div className="grid grid-cols-2 gap-3 p-3 sm:grid-cols-3 md:grid-cols-4">
          {albums.map((a) => (
            <AlbumCard key={a.id} album={a} />
          ))}
          {q.hasNextPage && (
            <div ref={sentinel} className="col-span-full h-8" data-testid="albums-sentinel" />
          )}
        </div>
      )}
    </>
  );
}
