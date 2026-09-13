import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthContextProvider } from "../auth/context";
import type { AuthProvider, AuthState } from "../auth/provider";
import { ApiProvider } from "../api/context";
import { Profile } from "./profile";
import { Forbidden } from "./forbidden";
import { fakeApi } from "../test-utils";
import type { ApiClient } from "../api/client";
import { test, expect, vi } from "vitest";

const signedIn: AuthState = {
  kind: "signed-in",
  uid: "u1",
  email: "a@example.com",
  displayName: "Alice",
};

function provider(signOut = vi.fn().mockResolvedValue(undefined)): AuthProvider {
  return {
    async init() {},
    onChange(cb) {
      cb(signedIn);
      return () => {};
    },
    async signIn() {},
    signOut,
    async getIdToken() {
      return "t";
    },
  };
}

function renderRoute(node: React.ReactNode, auth: AuthProvider, client: ApiClient) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <MemoryRouter>
      <AuthContextProvider provider={auth}>
        <QueryClientProvider client={qc}>
          <ApiProvider client={client}>{node}</ApiProvider>
        </QueryClientProvider>
      </AuthContextProvider>
    </MemoryRouter>,
  );
}

test("profile shows identity and role", async () => {
  renderRoute(
    <Profile />,
    provider(),
    fakeApi({ "/me": { uid: "u1", email: "a@example.com", role: "admin" } }),
  );
  expect(await screen.findByText("Alice")).toBeInTheDocument();
  expect(screen.getByText("a@example.com")).toBeInTheDocument();
  expect(screen.getByText(/Role: admin/)).toBeInTheDocument();
});

test("log out calls the auth provider", async () => {
  const signOut = vi.fn().mockResolvedValue(undefined);
  renderRoute(
    <Profile />,
    provider(signOut),
    fakeApi({ "/me": { uid: "u1", email: "a@example.com", role: "member" } }),
  );
  fireEvent.click(await screen.findByText("Log out"));
  await waitFor(() => expect(signOut).toHaveBeenCalled());
});

test("forbidden explains the allowlist and offers log out", async () => {
  const signOut = vi.fn().mockResolvedValue(undefined);
  renderRoute(<Forbidden />, provider(signOut), fakeApi({}));
  expect(screen.getByText("Access denied")).toBeInTheDocument();
  fireEvent.click(screen.getByText("Log out"));
  await waitFor(() => expect(signOut).toHaveBeenCalled());
});
