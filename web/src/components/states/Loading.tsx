export function Loading({ label = "Loading…" }: { label?: string }) {
  return (
    <div
      role="status"
      aria-live="polite"
      className="flex items-center justify-center p-8 text-sm text-gray-500"
    >
      {label}
    </div>
  );
}
