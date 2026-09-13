import { screen, waitFor } from "@testing-library/react";
import { Routes, Route } from "react-router-dom";
import { Albums } from "./albums";
import { AlbumDetail } from "./album-detail";
import { fakeApi, renderWithProviders } from "../test-utils";
import { test, expect } from "vitest";

const album = (id: number, cover = true) => ({
  id,
  category_id: 1,
  name: `album-${id}`,
  relative_path: `travel/album-${id}`,
  ...(cover ? { cover_photo_id: id * 10, cover_thumbnail_key: `k-${id}` } : {}),
});

const photo = (id: number) => ({
  id,
  album_id: 1,
  filename: `p${id}.jpg`,
  relative_path: `a/p${id}.jpg`,
  mime_type: "image/jpeg",
  thumbnail_key: `k-${id}`,
  file_size: 1,
  file_mtime_ns: 1,
});

test("lists albums with their covers", async () => {
  renderWithProviders(<Albums />, {
    client: fakeApi({ "/albums": { items: [album(1), album(2)], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(2));
  expect(await screen.findByAltText("album-1")).toBeInTheDocument();
  expect(screen.getAllByRole("link")[0]).toHaveAttribute("href", "/albums/1");
});

test("an album with no cover shows a placeholder", async () => {
  renderWithProviders(<Albums />, {
    client: fakeApi({ "/albums": { items: [album(1, false)], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getByText("no cover")).toBeInTheDocument());
});

test("no albums yields the empty state", async () => {
  renderWithProviders(<Albums />, {
    client: fakeApi({ "/albums": { items: [], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getByText(/No albums yet/)).toBeInTheDocument());
});

function renderDetail(client: ReturnType<typeof fakeApi>) {
  return renderWithProviders(
    <Routes>
      <Route path="/albums/:albumId" element={<AlbumDetail />} />
    </Routes>,
    { client, route: "/albums/7" },
  );
}

test("album detail shows the album name and its photos", async () => {
  renderDetail(
    fakeApi({
      "/albums/7/photos": { items: [photo(1), photo(2)], next_cursor: "" },
      "/albums/7": album(7),
    }),
  );
  await waitFor(() => expect(screen.getByRole("heading")).toHaveTextContent("album-7"));
  expect(screen.getAllByRole("link")).toHaveLength(2);
});

test("a missing album reports not-found rather than a network error", async () => {
  renderDetail(
    fakeApi({
      "/albums/7/photos": Object.assign(new Error("nf"), { status: 404 }),
      "/albums/7": Object.assign(new Error("nf"), { status: 404 }),
    }),
  );
  expect(await screen.findByRole("alert")).toHaveTextContent(/could not be found/);
});

test("an empty album says so", async () => {
  renderDetail(
    fakeApi({ "/albums/7/photos": { items: [], next_cursor: "" }, "/albums/7": album(7) }),
  );
  await waitFor(() => expect(screen.getByText("Album is empty")).toBeInTheDocument());
});
