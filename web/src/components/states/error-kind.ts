/** Maps an ApiError-ish rejection onto the panel copy that fits it. */
export function errorKind(e: unknown): "network" | "not-found" | "forbidden" {
  const status = (e as { status?: number } | null)?.status;
  if (status === 404) return "not-found";
  if (status === 403) return "forbidden";
  return "network";
}
