import type { ReactNode } from "react";

export function Header({ title, actions }: { title: string; actions?: ReactNode }) {
  return (
    <header className="sticky top-0 z-10 flex items-center justify-between gap-3 border-b bg-white/90 px-4 py-3 backdrop-blur dark:border-gray-800 dark:bg-gray-900/90">
      <h1 className="truncate text-base font-semibold">{title}</h1>
      {actions}
    </header>
  );
}
