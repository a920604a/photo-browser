import { render, screen } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { AuthContextProvider } from "../auth/context";
import type { AuthProvider, AuthState } from "../auth/provider";
import { ApiProvider } from "../api/context";
import type { ApiClient } from "../api/client";
import { ProtectedShell } from "./protected";
import { test, expect, vi } from "vitest";

function fake(state: AuthState): AuthProvider {
  return {
    async init() {},
    onChange(cb) {
      cb(state);
      return () => {};
    },
    async signIn() {},
    async signOut() {},
    async getIdToken() {
      return "t";
    },
  };
}

function renderWith(auth: AuthProvider, meResponse: () => unknown) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const client: ApiClient = {
    getJson: vi.fn().mockImplementation(meResponse),
    getBlob: vi.fn(),
  };
  return render(
    <MemoryRouter initialEntries={["/x"]}>
      <AuthContextProvider provider={auth}>
        <QueryClientProvider client={qc}>
          <ApiProvider client={client}>
            <Routes>
              <Route path="/login" element={<div>LOGIN</div>} />
              <Route path="/forbidden" element={<div>FORBIDDEN</div>} />
              <Route path="/x" element={<ProtectedShell />}>
                <Route index element={<div>SECRET</div>} />
              </Route>
            </Routes>
          </ApiProvider>
        </QueryClientProvider>
      </AuthContextProvider>
    </MemoryRouter>,
  );
}

const signedIn: AuthState = { kind: "signed-in", uid: "u", email: null, displayName: null };

test("signed-out redirects to /login", async () => {
  renderWith(fake({ kind: "signed-out" }), () =>
    Promise.resolve({ uid: "x", email: "", role: "admin" }),
  );
  expect(await screen.findByText("LOGIN")).toBeInTheDocument();
});

test("403 redirects to /forbidden", async () => {
  renderWith(fake(signedIn), () =>
    Promise.reject(Object.assign(new Error("no"), { status: 403 })),
  );
  expect(await screen.findByText("FORBIDDEN")).toBeInTheDocument();
});

test("401 from /me redirects to /login", async () => {
  renderWith(fake(signedIn), () =>
    Promise.reject(Object.assign(new Error("no"), { status: 401 })),
  );
  expect(await screen.findByText("LOGIN")).toBeInTheDocument();
});

test("success renders outlet", async () => {
  renderWith(fake(signedIn), () => Promise.resolve({ uid: "u", email: null, role: "admin" }));
  expect(await screen.findByText("SECRET")).toBeInTheDocument();
});

test("initializing shows a waiting state, not the login screen", () => {
  renderWith(fake({ kind: "initializing" }), () => Promise.resolve({}));
  expect(screen.getByRole("status")).toHaveTextContent("Signing in…");
  expect(screen.queryByText("LOGIN")).not.toBeInTheDocument();
});
