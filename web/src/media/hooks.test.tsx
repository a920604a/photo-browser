import { render, screen, waitFor } from "@testing-library/react";
import { MediaProvider, useAuthedImage, mediaPaths } from "./hooks";
import type { ApiClient } from "../api/client";
import { test, expect, vi, beforeEach } from "vitest";

beforeEach(() => {
  (globalThis.URL.createObjectURL as unknown) = vi.fn().mockReturnValue("blob://x");
  (globalThis.URL.revokeObjectURL as unknown) = vi.fn();
});

function okClient(): ApiClient {
  return {
    getJson: vi.fn(),
    getBlob: vi.fn().mockResolvedValue(new Blob([new Uint8Array([1])], { type: "image/webp" })),
  };
}

function Probe({ path }: { path: string | null }) {
  const s = useAuthedImage(path);
  return <div data-testid="s">{s.state === "error" ? `error:${s.kind}` : s.state}</div>;
}

test("resolves to ready", async () => {
  render(
    <MediaProvider api={okClient()}>
      <Probe path="/thumb/1" />
    </MediaProvider>,
  );
  await waitFor(() => expect(screen.getByTestId("s")).toHaveTextContent("ready"));
});

test("maps a 404 to not-found", async () => {
  const api: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi.fn().mockRejectedValue(Object.assign(new Error("nf"), { status: 404 })),
  };
  render(
    <MediaProvider api={api}>
      <Probe path="/thumb/1" />
    </MediaProvider>,
  );
  await waitFor(() => expect(screen.getByTestId("s")).toHaveTextContent("error:not-found"));
});

test("maps an unknown failure to network", async () => {
  const api: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi.fn().mockRejectedValue(new Error("offline")),
  };
  render(
    <MediaProvider api={api}>
      <Probe path="/thumb/1" />
    </MediaProvider>,
  );
  await waitFor(() => expect(screen.getByTestId("s")).toHaveTextContent("error:network"));
});

test("a null path stays idle and fetches nothing", async () => {
  const api = okClient();
  render(
    <MediaProvider api={api}>
      <Probe path={null} />
    </MediaProvider>,
  );
  expect(screen.getByTestId("s")).toHaveTextContent("loading");
  expect(api.getBlob).not.toHaveBeenCalled();
});

test("media paths match the api routes", () => {
  expect(mediaPaths.thumbnail(7, "k-1")).toBe("/photos/7/thumbnail/k-1");
  expect(mediaPaths.original(7)).toBe("/photos/7/original");
});
