import { useEffect, useState } from "react";
import { AuthContextProvider } from "./auth/context";
import type { AuthProvider } from "./auth/provider";
import { makeAuthProvider } from "./auth/select";

export function App() {
  const [provider, setProvider] = useState<AuthProvider | null>(null);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    makeAuthProvider()
      .then(setProvider)
      .catch((e: unknown) => setError(String(e)));
  }, []);
  if (error) return <div className="p-6 text-red-600">Auth error: {error}</div>;
  if (!provider) return <div className="p-6">Loading…</div>;
  return (
    <AuthContextProvider provider={provider}>
      <div className="p-6">Signed in / out screen goes here</div>
    </AuthContextProvider>
  );
}
