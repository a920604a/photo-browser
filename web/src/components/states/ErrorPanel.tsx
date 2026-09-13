type Kind = "network" | "not-found" | "forbidden" | "pagination";
type Props = { kind: Kind; onRetry?: () => void };

const copy: Record<Kind, string> = {
  network: "Cannot reach the server right now.",
  "not-found": "This item could not be found.",
  forbidden: "You do not have permission to view this.",
  pagination: "Failed to load more items.",
};

export function ErrorPanel({ kind, onRetry }: Props) {
  return (
    <div role="alert" className="flex flex-col items-center justify-center gap-3 p-8 text-center">
      <p className="text-sm text-gray-700 dark:text-gray-200">{copy[kind]}</p>
      {onRetry && (
        <button
          onClick={onRetry}
          className="min-h-touch min-w-touch rounded border px-4 py-2 text-sm hover:bg-gray-100 dark:hover:bg-gray-800"
        >
          Retry
        </button>
      )}
    </div>
  );
}
