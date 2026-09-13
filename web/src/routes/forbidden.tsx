import { useNavigate } from "react-router-dom";
import { useAuth } from "../auth/context";

export function Forbidden() {
  const nav = useNavigate();
  const { provider } = useAuth();
  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col items-center justify-center gap-6 p-6 text-center">
      <h1 className="text-lg font-semibold">Access denied</h1>
      <p className="text-sm text-gray-600 dark:text-gray-300">
        Your account is signed in, but is not on the allowlist for this photo library.
      </p>
      <button
        onClick={() => {
          void provider.signOut().then(() => nav("/login", { replace: true }));
        }}
        className="min-h-touch rounded border px-4 py-2 text-sm"
      >
        Log out
      </button>
    </main>
  );
}
