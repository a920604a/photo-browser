import { useEffect, useState } from "react";
import { useNavigate } from "react-router-dom";
import { useAuth } from "../auth/context";
import { env } from "../lib/env";

export function Login() {
  const { state, provider } = useAuth();
  const nav = useNavigate();
  const [err, setErr] = useState<string | null>(null);
  const [uid, setUid] = useState<string>(env.devUsers[0]?.uid ?? "");

  useEffect(() => {
    if (state.kind === "signed-in") nav("/photos", { replace: true });
  }, [state.kind, nav]);

  return (
    <main className="mx-auto flex min-h-screen max-w-sm flex-col items-center justify-center gap-6 p-6">
      <h1 className="text-xl font-semibold">Photo Browser</h1>
      <p className="text-center text-sm text-gray-500">
        Private family photo library — sign in to continue.
      </p>

      {env.authMode === "firebase" ? (
        <button
          onClick={() => void provider.signIn().catch((e: unknown) => setErr(String(e)))}
          className="min-h-touch min-w-touch rounded bg-blue-600 px-5 py-3 text-white"
        >
          Sign in with Google
        </button>
      ) : (
        <>
          <label className="text-sm">
            Dev user
            <select
              value={uid}
              onChange={(e) => setUid(e.target.value)}
              className="ml-2 min-h-touch rounded border px-2 py-1"
            >
              {env.devUsers.map((u) => (
                <option key={u.uid} value={u.uid}>
                  {u.email} ({u.role})
                </option>
              ))}
            </select>
          </label>
          <button
            onClick={() => void provider.signIn(uid).catch((e: unknown) => setErr(String(e)))}
            className="min-h-touch min-w-touch rounded bg-blue-600 px-5 py-3 text-white"
          >
            Sign in
          </button>
        </>
      )}
      {err && (
        <p role="alert" className="text-sm text-red-600">
          {err}
        </p>
      )}
    </main>
  );
}
