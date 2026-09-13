import { FirebaseAuthProvider } from "./firebase";
import { vi, test, expect, beforeEach } from "vitest";

const mocks = vi.hoisted(() => ({
  initializeApp: vi.fn(),
  getAuth: vi.fn(),
  GoogleAuthProvider: vi.fn(),
  signInWithPopup: vi.fn(),
  signOut: vi.fn(),
  onIdTokenChanged: vi.fn(),
}));

vi.mock("firebase/app", () => ({ initializeApp: mocks.initializeApp }));
vi.mock("firebase/auth", () => ({
  getAuth: mocks.getAuth,
  GoogleAuthProvider: mocks.GoogleAuthProvider,
  signInWithPopup: mocks.signInWithPopup,
  signOut: mocks.signOut,
  onIdTokenChanged: mocks.onIdTokenChanged,
}));

const cfg = { apiKey: "k", authDomain: "d", projectId: "p", appId: "a" };

beforeEach(() => {
  Object.values(mocks).forEach((m) => m.mockReset());
});

/** Captures the onIdTokenChanged callback so a test can drive auth state. */
function captureTokenListener(): () => (u: unknown) => void {
  let cb: ((u: unknown) => void) | null = null;
  mocks.onIdTokenChanged.mockImplementation((_auth: unknown, fn: (u: unknown) => void) => {
    cb = fn;
    return () => {};
  });
  mocks.getAuth.mockReturnValue({});
  return () => {
    if (!cb) throw new Error("onIdTokenChanged was never subscribed");
    return cb;
  };
}

test("emits signed-in when Firebase reports a user", async () => {
  const user = {
    uid: "abc",
    email: "a@x",
    displayName: "Alice",
    getIdToken: vi.fn().mockResolvedValue("tok"),
  };
  const listener = captureTokenListener();
  const p = new FirebaseAuthProvider(cfg);
  const states: string[] = [];
  await p.init();
  p.onChange((s) => states.push(s.kind));
  listener()(user);
  expect(states.at(-1)).toBe("signed-in");
  expect(await p.getIdToken()).toBe("tok");
  expect(user.getIdToken).toHaveBeenCalledWith(false);
});

test("getIdToken(true) forces refresh", async () => {
  const user = {
    uid: "abc",
    email: null,
    displayName: null,
    getIdToken: vi.fn().mockResolvedValue("fresh"),
  };
  const listener = captureTokenListener();
  const p = new FirebaseAuthProvider(cfg);
  await p.init();
  p.onChange(() => {});
  listener()(user);
  await p.getIdToken(true);
  expect(user.getIdToken).toHaveBeenCalledWith(true);
});

test("emits signed-out when Firebase reports no user", async () => {
  const listener = captureTokenListener();
  const p = new FirebaseAuthProvider(cfg);
  const states: string[] = [];
  await p.init();
  p.onChange((s) => states.push(s.kind));
  listener()(null);
  expect(states.at(-1)).toBe("signed-out");
  expect(await p.getIdToken()).toBeNull();
});

test("signIn opens the Google popup", async () => {
  captureTokenListener();
  const p = new FirebaseAuthProvider(cfg);
  await p.init();
  await p.signIn();
  expect(mocks.signInWithPopup).toHaveBeenCalledTimes(1);
  expect(mocks.GoogleAuthProvider).toHaveBeenCalledTimes(1);
});

test("init rejects without config", async () => {
  const p = new FirebaseAuthProvider({ ...cfg, apiKey: "" });
  await expect(p.init()).rejects.toThrow(/missing config/);
});
