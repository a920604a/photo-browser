import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { AuthContextProvider } from "../auth/context";
import type { AuthProvider, AuthState } from "../auth/provider";
import { Login } from "./login";
import { env } from "../lib/env";
import { test, expect, vi } from "vitest";

const fakeProvider = (
  opts: Partial<{ onSignIn: (arg?: unknown) => Promise<void>; state: AuthState }> = {},
): AuthProvider => ({
  async init() {},
  onChange(cb) {
    cb(opts.state ?? { kind: "signed-out" });
    return () => {};
  },
  signIn: opts.onSignIn ?? (async () => {}),
  async signOut() {},
  async getIdToken() {
    return null;
  },
});

function renderLogin(provider: AuthProvider) {
  return render(
    <MemoryRouter>
      <AuthContextProvider provider={provider}>
        <Login />
      </AuthContextProvider>
    </MemoryRouter>,
  );
}

test("dev mode calls signIn with selected uid", async () => {
  const spy = vi.fn().mockResolvedValue(undefined);
  renderLogin(fakeProvider({ onSignIn: spy }));
  fireEvent.click(screen.getByText(/^Sign in$/));
  await waitFor(() => expect(spy).toHaveBeenCalledWith(env.devUsers[0]?.uid ?? ""));
});

test("dev mode lists every seeded dev user", () => {
  renderLogin(fakeProvider());
  const select = screen.getByLabelText(/Dev user/);
  expect(select).toBeInTheDocument();
  expect(screen.getAllByRole("option")).toHaveLength(env.devUsers.length);
});

test("switching the dropdown signs in as that user", async () => {
  const spy = vi.fn().mockResolvedValue(undefined);
  renderLogin(fakeProvider({ onSignIn: spy }));
  const other = env.devUsers[1];
  if (!other) throw new Error("test needs at least two VITE_DEV_USERS");
  fireEvent.change(screen.getByLabelText(/Dev user/), { target: { value: other.uid } });
  fireEvent.click(screen.getByText(/^Sign in$/));
  await waitFor(() => expect(spy).toHaveBeenCalledWith(other.uid));
});

test("a failed sign-in surfaces an alert", async () => {
  const spy = vi.fn().mockRejectedValue(new Error("mint down"));
  renderLogin(fakeProvider({ onSignIn: spy }));
  fireEvent.click(screen.getByText(/^Sign in$/));
  expect(await screen.findByRole("alert")).toHaveTextContent("mint down");
});
