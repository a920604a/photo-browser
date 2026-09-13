import { Link } from "react-router-dom";
import type { Category } from "../../api/types";

export function CategoryCard({ category }: { category: Category }) {
  return (
    <Link
      to={`/categories/${category.id}`}
      className="min-h-touch flex items-center justify-between rounded border bg-white px-4 py-4 shadow-sm hover:bg-gray-50 dark:border-gray-800 dark:bg-gray-900 dark:hover:bg-gray-800"
    >
      <span className="text-sm font-medium">{category.name}</span>
      <span aria-hidden="true">›</span>
    </Link>
  );
}
