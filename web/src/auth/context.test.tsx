import { render, screen, act } from "@testing-library/react";
import { AuthContextProvider, useAuth } from "./context";
import type { AuthProvider, AuthState } from "./provider";

class FakeProvider implements AuthProvider {
  private listener: ((s: AuthState) => void) | null = null;
  state: AuthState = { kind: "initializing" };
  async init() {}
  onChange(cb: (s: AuthState) => void) {
    this.listener = cb;
    cb(this.state);
    return () => {
      this.listener = null;
    };
  }
  emit(s: AuthState) {
    this.state = s;
    this.listener?.(s);
  }
  async signIn() {}
  async signOut() {}
  async getIdToken() {
    return "t";
  }
}

function Probe() {
  const { state } = useAuth();
  return <span data-testid="state">{state.kind}</span>;
}

test("mirrors provider state transitions", async () => {
  const fp = new FakeProvider();
  render(
    <AuthContextProvider provider={fp}>
      <Probe />
    </AuthContextProvider>,
  );
  expect(screen.getByTestId("state")).toHaveTextContent("initializing");
  await act(async () => fp.emit({ kind: "signed-out" }));
  expect(screen.getByTestId("state")).toHaveTextContent("signed-out");
  await act(async () =>
    fp.emit({
      kind: "signed-in",
      uid: "u1",
      email: "e@x",
      displayName: "N",
    }),
  );
  expect(screen.getByTestId("state")).toHaveTextContent("signed-in");
});

test("useAuth outside the provider is a programming error", () => {
  const err = vi.spyOn(console, "error").mockImplementation(() => {});
  expect(() => render(<Probe />)).toThrow(/AuthContextProvider/);
  err.mockRestore();
});
