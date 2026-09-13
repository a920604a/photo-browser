import { Header } from "../components/layout/Header";
import { CategoryCard } from "../components/grid/CategoryCard";
import { Loading } from "../components/states/Loading";
import { Empty } from "../components/states/Empty";
import { ErrorPanel } from "../components/states/ErrorPanel";
import { errorKind } from "../components/states/error-kind";
import { useCategories } from "../api/queries";

export function Categories() {
  const q = useCategories();
  const cats = q.data?.items ?? [];
  return (
    <>
      <Header title="Categories" />
      {q.isLoading ? (
        <Loading />
      ) : q.error ? (
        <ErrorPanel kind={errorKind(q.error)} onRetry={() => void q.refetch()} />
      ) : cats.length === 0 ? (
        <Empty title="No categories yet" hint="Categories are the top-level folders of your library." />
      ) : (
        <div className="flex flex-col gap-2 p-3">
          {cats.map((c) => (
            <CategoryCard key={c.id} category={c} />
          ))}
        </div>
      )}
    </>
  );
}
