import { screen, waitFor } from "@testing-library/react";
import { Routes, Route } from "react-router-dom";
import { Viewer } from "./viewer";
import { fakeApi, renderWithProviders } from "../test-utils";
import { test, expect } from "vitest";

const photo = (id: number, taken?: string) => ({
  id,
  album_id: 5,
  filename: `p${id}.jpg`,
  relative_path: `a/p${id}.jpg`,
  mime_type: "image/jpeg",
  thumbnail_key: `k-${id}`,
  taken_at: taken,
  file_size: 1,
  file_mtime_ns: 1,
});

function renderViewer(client: ReturnType<typeof fakeApi>, route: string) {
  return renderWithProviders(
    <Routes>
      <Route path="/viewer/:photoId" element={<Viewer />} />
    </Routes>,
    { client, route },
  );
}

test("from=all reads the all-photos collection", async () => {
  const client = fakeApi({ "/photos": { items: [photo(1), photo(2)], next_cursor: "" } });
  renderViewer(client, "/viewer/2?from=all");
  await waitFor(() => expect(screen.getByRole("dialog")).toHaveAccessibleName("p2.jpg"));
  expect(screen.getByLabelText("Previous")).toHaveAttribute("href", "/viewer/1?from=all");
});

test("from=album-N reads that album and skips the other collections", async () => {
  const client = fakeApi({
    "/albums/5/photos": { items: [photo(7), photo(8)], next_cursor: "" },
  });
  renderViewer(client, "/viewer/8?from=album-5");
  await waitFor(() => expect(screen.getByRole("dialog")).toHaveAccessibleName("p8.jpg"));
  const paths = (client.getJson as unknown as { mock: { calls: string[][] } }).mock.calls.map(
    (c) => c[0],
  );
  expect(paths).toEqual(["/albums/5/photos"]);
});

test("from=t<month> reads the timeline", async () => {
  const client = fakeApi({
    "/photos/timeline": { items: [photo(3, "2025-05-04T10:00:00")], next_cursor: "" },
  });
  renderViewer(client, "/viewer/3?from=t2025-05");
  await waitFor(() => expect(screen.getByRole("dialog")).toHaveAccessibleName("p3.jpg"));
});

test("a deep link outside the loaded pages falls back to fetching the photo", async () => {
  const client = fakeApi({
    "/photos/99": photo(99),
    "/photos": { items: [photo(1)], next_cursor: "" },
  });
  renderViewer(client, "/viewer/99?from=all");
  await waitFor(() => expect(screen.getByRole("dialog")).toHaveAccessibleName("p99.jpg"));
  expect(screen.queryByLabelText("Next")).not.toBeInTheDocument();
});

test("an unknown photo id reports not-found", async () => {
  const client = fakeApi({
    "/photos/99": Object.assign(new Error("nf"), { status: 404 }),
    "/photos": { items: [photo(1)], next_cursor: "" },
  });
  renderViewer(client, "/viewer/99?from=all");
  expect(await screen.findByRole("alert")).toHaveTextContent(/could not be found/);
});
