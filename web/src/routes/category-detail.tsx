import { useParams } from "react-router-dom";
import { useCategories, useCategoryAlbums } from "../api/queries";
import { Header } from "../components/layout/Header";
import { Loading } from "../components/states/Loading";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { errorKind } from "../components/states/error-kind";
import { Empty } from "../components/states/Empty";
import { AlbumCard } from "../components/grid/AlbumCard";

export function CategoryDetail() {
  const { categoryId } = useParams();
  const id = Number(categoryId);
  const q = useCategoryAlbums(id);
  // /categories is small and already cached by the list route, so reusing it
  // beats adding a GET /categories/{id} the API does not have.
  const cats = useCategories();
  const name = cats.data?.items.find((c) => c.id === id)?.name;
  const albums = q.data?.items ?? [];
  return (
    <>
      <Header title={name ?? "Category"} />
      {q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <ErrorPanel kind={errorKind(q.error)} onRetry={() => void q.refetch()} />
      ) : albums.length === 0 ? (
        <Empty title="No albums in this category" />
      ) : (
        <div className="grid grid-cols-2 gap-3 p-3 sm:grid-cols-3 md:grid-cols-4">
          {albums.map((a) => (
            <AlbumCard key={a.id} album={a} />
          ))}
        </div>
      )}
    </>
  );
}
