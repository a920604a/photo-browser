import { NavLink } from "react-router-dom";

type Item = { to: string; label: string; end?: boolean };

export function Tabs({ items }: { items: Item[] }) {
  return (
    <div role="tablist" className="flex gap-1 border-b bg-white p-2 dark:border-gray-800 dark:bg-gray-900">
      {items.map((it) => (
        <NavLink
          key={it.to}
          to={it.to}
          end={it.end}
          role="tab"
          className={({ isActive }) =>
            "min-h-touch rounded px-3 py-2 text-sm " +
            (isActive
              ? "bg-blue-600 text-white"
              : "text-gray-700 hover:bg-gray-100 dark:text-gray-200 dark:hover:bg-gray-800")
          }
        >
          {it.label}
        </NavLink>
      ))}
    </div>
  );
}
