import { createContext, useContext, type ReactNode } from "react";
import type { ApiClient } from "./client";

const ApiCtx = createContext<ApiClient | null>(null);

export function ApiProvider({ client, children }: { client: ApiClient; children: ReactNode }) {
  return <ApiCtx.Provider value={client}>{children}</ApiCtx.Provider>;
}

export function useApi(): ApiClient {
  const c = useContext(ApiCtx);
  if (!c) throw new Error("useApi must be inside ApiProvider");
  return c;
}
