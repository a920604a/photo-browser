import { useAuth } from "../auth/context";
import { useMe } from "../api/queries";
import { Header } from "../components/layout/Header";
import { Loading } from "../components/states/Loading";

export function Profile() {
  const { state, provider } = useAuth();
  const me = useMe();
  if (me.isLoading || state.kind === "initializing") return <Loading />;
  const email = state.kind === "signed-in" ? (state.email ?? me.data?.email ?? "") : "";
  const name = state.kind === "signed-in" ? (state.displayName ?? "") : "";
  const role = me.data?.role ?? "";
  return (
    <>
      <Header title="Profile" />
      <div className="space-y-4 p-4">
        {name && <p className="text-lg font-semibold">{name}</p>}
        {email && <p className="text-sm text-gray-600 dark:text-gray-300">{email}</p>}
        {role && <p className="text-xs uppercase tracking-wide text-gray-500">Role: {role}</p>}
        <button
          onClick={() => void provider.signOut()}
          className="min-h-touch rounded border px-4 py-2 text-sm"
        >
          Log out
        </button>
      </div>
    </>
  );
}
