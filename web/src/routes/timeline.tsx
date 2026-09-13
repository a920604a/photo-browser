import { Header } from "../components/layout/Header";
import { Tabs } from "../components/ui/Tabs";
import { PhotoGrid } from "../components/grid/PhotoGrid";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { useTimeline } from "../api/queries";
import { groupByYearMonth } from "../lib/fmt";
import { photoTabs } from "./photos";

export function Timeline() {
  const q = useTimeline();
  const all = q.data?.pages.flatMap((p) => p.items) ?? [];
  const groups = groupByYearMonth(all);
  return (
    <>
      <Header title="Photos" />
      <Tabs items={photoTabs} />
      {q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <ErrorPanel kind="network" onRetry={() => void q.refetch()} />
      ) : groups.length === 0 ? (
        <Empty title="No dated photos yet" hint="Photos need an EXIF date to appear here." />
      ) : (
        <div>
          {groups.map((g) => (
            <section key={g.key} className="py-2">
              <h2 className="px-3 py-2 text-sm font-semibold text-gray-600 dark:text-gray-300">
                {g.label}
              </h2>
              {/* Pagination lives on the page, not per-group, so each grid is fixed. */}
              <PhotoGrid
                photos={g.photos}
                from={`t${g.key}`}
                hasMore={false}
                onLoadMore={() => {}}
              />
            </section>
          ))}
          {q.hasNextPage && (
            <div className="p-4 text-center">
              <button
                onClick={() => void q.fetchNextPage()}
                disabled={q.isFetchingNextPage}
                className="min-h-touch rounded border px-4 py-2 text-sm disabled:opacity-50"
              >
                {q.isFetchingNextPage ? "Loading…" : "Load more"}
              </button>
            </div>
          )}
        </div>
      )}
    </>
  );
}
