import { createContext, useContext, useEffect, useState, type ReactNode } from "react";
import type { AuthProvider, AuthState } from "./provider";

type Ctx = { state: AuthState; provider: AuthProvider };

const AuthCtx = createContext<Ctx | null>(null);

/**
 * Mirrors the injected provider's auth state into React. Stays on
 * "initializing" until the provider reports, so route guards can wait for a
 * settled answer instead of flashing the login screen.
 */
export function AuthContextProvider({
  provider,
  children,
}: {
  provider: AuthProvider;
  children: ReactNode;
}) {
  const [state, setState] = useState<AuthState>({ kind: "initializing" });
  useEffect(() => {
    let cancelled = false;
    provider.init().catch(() => {
      if (!cancelled) setState({ kind: "signed-out" });
    });
    const off = provider.onChange((s) => {
      if (!cancelled) setState(s);
    });
    return () => {
      cancelled = true;
      off();
    };
  }, [provider]);
  return <AuthCtx.Provider value={{ state, provider }}>{children}</AuthCtx.Provider>;
}

export function useAuth(): Ctx {
  const ctx = useContext(AuthCtx);
  if (!ctx) throw new Error("useAuth must be inside AuthContextProvider");
  return ctx;
}
