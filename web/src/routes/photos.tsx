import { Header } from "../components/layout/Header";
import { Tabs } from "../components/ui/Tabs";
import { PhotoGrid } from "../components/grid/PhotoGrid";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { useAllPhotos } from "../api/queries";

export const photoTabs = [
  { to: "/photos", label: "All", end: true },
  { to: "/photos/timeline", label: "Timeline" },
];

export function Photos() {
  const q = useAllPhotos();
  const photos = q.data?.pages.flatMap((p) => p.items) ?? [];
  return (
    <>
      <Header title="Photos" />
      <Tabs items={photoTabs} />
      {q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <ErrorPanel kind="network" onRetry={() => void q.refetch()} />
      ) : photos.length === 0 ? (
        <Empty title="No photos yet" hint="Add photos to your NAS and re-run the indexer." />
      ) : (
        <PhotoGrid
          photos={photos}
          from="all"
          hasMore={!!q.hasNextPage}
          onLoadMore={() => void q.fetchNextPage()}
        />
      )}
    </>
  );
}
