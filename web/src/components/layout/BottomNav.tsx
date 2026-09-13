import { NavLink } from "react-router-dom";

const items = [
  { to: "/photos", label: "Photos" },
  { to: "/albums", label: "Albums" },
  { to: "/categories", label: "Categories" },
  { to: "/profile", label: "Profile" },
];

export function BottomNav() {
  return (
    <nav
      aria-label="Primary"
      className="fixed inset-x-0 bottom-0 z-10 flex justify-around border-t bg-white/90 pb-[env(safe-area-inset-bottom)] backdrop-blur dark:border-gray-800 dark:bg-gray-900/90"
    >
      {items.map((it) => (
        <NavLink
          key={it.to}
          to={it.to}
          className={({ isActive }) =>
            "min-h-touch min-w-touch flex-1 py-3 text-center text-xs " +
            (isActive ? "font-semibold text-blue-600" : "text-gray-600 dark:text-gray-300")
          }
        >
          {it.label}
        </NavLink>
      ))}
    </nav>
  );
}
