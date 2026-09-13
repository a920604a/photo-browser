import { useEffect, useMemo, useState } from "react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthContextProvider } from "./auth/context";
import type { AuthProvider } from "./auth/provider";
import { makeAuthProvider } from "./auth/select";
import { makeApi } from "./api/client";
import { ApiProvider } from "./api/context";
import { MediaProvider } from "./media/hooks";
import { AppRouter } from "./routes/router";
import { env } from "./lib/env";

export function App() {
  const [provider, setProvider] = useState<AuthProvider | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    makeAuthProvider()
      .then(setProvider)
      .catch((e: unknown) => setError(String(e)));
  }, []);
  const qc = useMemo(
    () =>
      new QueryClient({
        defaultOptions: {
          queries: {
            // 401/403/404 are answers, not blips — retrying them only delays
            // the screen the user should see.
            retry: (n, e) => n < 2 && !isTerminalStatus(e),
            staleTime: 30_000,
            gcTime: 5 * 60_000,
          },
        },
      }),
    [],
  );
  const client = useMemo(
    () => (provider ? makeApi(provider, env.apiBaseUrl) : null),
    [provider],
  );
  if (error) return <div className="p-6 text-red-600">Auth error: {error}</div>;
  if (!provider || !client) return <div className="p-6">Loading…</div>;
  return (
    <AuthContextProvider provider={provider}>
      <QueryClientProvider client={qc}>
        <ApiProvider client={client}>
          <MediaProvider api={client}>
            <AppRouter />
          </MediaProvider>
        </ApiProvider>
      </QueryClientProvider>
    </AuthContextProvider>
  );
}

function isTerminalStatus(e: unknown): boolean {
  if (e && typeof e === "object" && "status" in e) {
    const s = (e as { status: number }).status;
    return s === 401 || s === 403 || s === 404;
  }
  return false;
}
