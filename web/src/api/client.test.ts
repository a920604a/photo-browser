import { makeApi, ApiError } from "./client";
import type { AuthProvider } from "../auth/provider";
import { vi, test, expect, beforeEach } from "vitest";

class FakeAuth implements AuthProvider {
  tokens: string[] = ["t1", "t2"];
  idx = 0;
  async init() {}
  onChange() {
    return () => {};
  }
  async signIn() {}
  async signOut() {}
  async getIdToken(force = false) {
    if (force) this.idx++;
    return this.tokens[this.idx] ?? null;
  }
}

/** Matches the backend's WriteError envelope. */
function errorResponse(status: number, code: string, message: string, requestId?: string) {
  return new Response(JSON.stringify({ error: { code, message } }), {
    status,
    headers: requestId ? { "X-Request-Id": requestId } : {},
  });
}

beforeEach(() => vi.restoreAllMocks());

test("getJson attaches bearer and parses body", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  const body = await api.getJson<{ ok: boolean }>("/me");
  expect(body).toEqual({ ok: true });
  const call = fetchMock.mock.calls[0];
  expect((call[1] as RequestInit).headers).toMatchObject({ Authorization: "Bearer t1" });
  expect(call[0]).toBe("/api/v1/me");
});

test("401 triggers refresh + retry once", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValueOnce(errorResponse(401, "unauthorized", "expired"))
    .mockResolvedValueOnce(new Response(JSON.stringify({ ok: true }), { status: 200 }));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  const body = await api.getJson<{ ok: boolean }>("/me");
  expect(body).toEqual({ ok: true });
  expect(fetchMock).toHaveBeenCalledTimes(2);
  expect((fetchMock.mock.calls[1][1] as RequestInit).headers).toMatchObject({
    Authorization: "Bearer t2",
  });
});

test("second 401 throws ApiError(401)", async () => {
  const fetchMock = vi
    .fn()
    .mockImplementation(() => Promise.resolve(errorResponse(401, "unauthorized", "bad", "r1")));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  await expect(api.getJson("/me")).rejects.toMatchObject({
    status: 401,
    code: "unauthorized",
    requestId: "r1",
  });
  expect(fetchMock).toHaveBeenCalledTimes(2);
});

test("403 throws ApiError without retry", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(errorResponse(403, "forbidden", "not on the allowlist", "abc"));
  globalThis.fetch = fetchMock as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  await expect(api.getJson("/me")).rejects.toBeInstanceOf(ApiError);
  expect(fetchMock).toHaveBeenCalledTimes(1);
});

test("404 carries code and request id", async () => {
  globalThis.fetch = vi
    .fn()
    .mockResolvedValue(errorResponse(404, "not_found", "photo not found", "rq9")) as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  await expect(api.getJson("/photos/1")).rejects.toMatchObject({
    status: 404,
    code: "not_found",
    requestId: "rq9",
    message: "photo not found",
  });
});

test("non-JSON error body still yields an ApiError", async () => {
  globalThis.fetch = vi
    .fn()
    .mockResolvedValue(new Response("<html>502</html>", { status: 502 })) as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  await expect(api.getJson("/me")).rejects.toMatchObject({ status: 502, code: "unknown" });
});

test("getBlob returns Blob for 200", async () => {
  const blob = new Blob([new Uint8Array([1, 2, 3])], { type: "image/webp" });
  // undici derives the blob type from the response headers, not the body.
  globalThis.fetch = vi.fn().mockResolvedValue(
    new Response(blob, { status: 200, headers: { "Content-Type": "image/webp" } }),
  ) as never;
  const api = makeApi(new FakeAuth(), "/api/v1");
  const out = await api.getBlob("/photos/1/thumbnail/abc");
  expect(out.type).toBe("image/webp");
});

test("a signed-out provider sends no Authorization header", async () => {
  const fetchMock = vi
    .fn()
    .mockResolvedValue(new Response(JSON.stringify({ ok: true }), { status: 200 }));
  globalThis.fetch = fetchMock as never;
  const auth = new FakeAuth();
  auth.tokens = [];
  const api = makeApi(auth, "/api/v1");
  await api.getJson("/me");
  expect((fetchMock.mock.calls[0][1] as RequestInit).headers).not.toHaveProperty("Authorization");
});
