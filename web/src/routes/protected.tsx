import { Navigate, Outlet } from "react-router-dom";
import { useAuth } from "../auth/context";
import { useMe } from "../api/queries";
import { Loading } from "../components/states/Loading";

/**
 * The route contract for everything behind sign-in: wait for auth to settle,
 * then wait for /me. A 403 there means authenticated but not on the allowlist,
 * which is a different screen from "not signed in".
 */
export function ProtectedShell() {
  const { state } = useAuth();
  const me = useMe();

  if (state.kind === "initializing") return <Loading label="Signing in…" />;
  if (state.kind === "signed-out") return <Navigate to="/login" replace />;

  if (me.isLoading) return <Loading label="Checking permissions…" />;
  if (me.error) {
    const status = (me.error as { status?: number }).status;
    if (status === 403) return <Navigate to="/forbidden" replace />;
    if (status === 401) return <Navigate to="/login" replace />;
    return <Loading label="Retrying…" />;
  }
  return <Outlet />;
}
