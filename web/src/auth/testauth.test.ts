import { TestAuthProvider } from "./testauth";
import { vi, beforeEach, afterEach, test, expect } from "vitest";

const now = 1_700_000_000_000;

beforeEach(() => {
  vi.useFakeTimers();
  vi.setSystemTime(now);
  sessionStorage.clear();
});
afterEach(() => vi.useRealTimers());

function mintJwt(exp: number, sub = "admin-1", email = "a@x"): string {
  const b64 = (o: unknown) =>
    btoa(JSON.stringify(o)).replaceAll("=", "").replaceAll("+", "-").replaceAll("/", "_");
  return `${b64({ alg: "RS256" })}.${b64({ sub, email, exp })}.sig`;
}

const devUsers = [{ uid: "admin-1", email: "a@x", role: "admin" as const }];

test("signIn mints and stores token", async () => {
  const jwt = mintJwt(Math.floor(now / 1000) + 3600);
  globalThis.fetch = vi.fn().mockResolvedValue(new Response(jwt, { status: 200 })) as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  await p.signIn("admin-1");
  expect(await p.getIdToken()).toBe(jwt);
  expect(sessionStorage.getItem("ta.token")).toBe(jwt);
});

test("getIdToken(true) re-mints", async () => {
  const jwt1 = mintJwt(Math.floor(now / 1000) + 3600);
  const jwt2 = mintJwt(Math.floor(now / 1000) + 7200);
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(new Response(jwt1))
    .mockResolvedValueOnce(new Response(jwt2));
  globalThis.fetch = fetchMock as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  await p.signIn("admin-1");
  expect(await p.getIdToken(true)).toBe(jwt2);
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test("expiry within skew triggers re-mint on get", async () => {
  const nearExpiry = mintJwt(Math.floor(now / 1000) + 5); // in 5s
  const fresh = mintJwt(Math.floor(now / 1000) + 3600);
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(new Response(nearExpiry))
    .mockResolvedValueOnce(new Response(fresh));
  globalThis.fetch = fetchMock as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  await p.signIn("admin-1");
  expect(await p.getIdToken()).toBe(fresh);
});

test("signOut clears storage and emits signed-out", async () => {
  globalThis.fetch = vi
    .fn()
    .mockResolvedValue(new Response(mintJwt(Math.floor(now / 1000) + 3600))) as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  await p.signIn("admin-1");
  const states: string[] = [];
  p.onChange((s) => states.push(s.kind));
  await p.signOut();
  expect(sessionStorage.getItem("ta.token")).toBeNull();
  expect(states.at(-1)).toBe("signed-out");
});

test("init restores a live session from sessionStorage", async () => {
  const jwt = mintJwt(Math.floor(now / 1000) + 3600);
  sessionStorage.setItem("ta.token", jwt);
  sessionStorage.setItem("ta.uid", "admin-1");
  globalThis.fetch = vi.fn() as never;
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  const states: string[] = [];
  p.onChange((s) => states.push(s.kind));
  expect(states).toEqual(["signed-in"]);
  expect(globalThis.fetch).not.toHaveBeenCalled();
});

test("init drops an expired stored session", async () => {
  sessionStorage.setItem("ta.token", mintJwt(Math.floor(now / 1000) - 10));
  sessionStorage.setItem("ta.uid", "admin-1");
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  expect(sessionStorage.getItem("ta.token")).toBeNull();
  const states: string[] = [];
  p.onChange((s) => states.push(s.kind));
  expect(states).toEqual(["signed-out"]);
});

test("mints against an absolute base url", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response(mintJwt(Math.floor(now / 1000) + 3600)));
  globalThis.fetch = fetchMock as never;
  const p = new TestAuthProvider({ url: "http://ta:8090", devUsers });
  await p.init();
  await p.signIn("admin-1");
  expect(fetchMock.mock.calls[0][0]).toBe("http://ta:8090/mint?sub=admin-1&email=a%40x&verified=1");
});

test("mints against a same-origin proxy path", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response(mintJwt(Math.floor(now / 1000) + 3600)));
  globalThis.fetch = fetchMock as never;
  const p = new TestAuthProvider({ url: "/testauth", devUsers });
  await p.init();
  await p.signIn("admin-1");
  expect(fetchMock.mock.calls[0][0]).toBe(
    `${location.origin}/testauth/mint?sub=admin-1&email=a%40x&verified=1`,
  );
});

test("signIn with an unknown uid rejects", async () => {
  const p = new TestAuthProvider({ url: "http://ta", devUsers });
  await p.init();
  await expect(p.signIn("nope")).rejects.toThrow(/unknown dev uid/);
});
