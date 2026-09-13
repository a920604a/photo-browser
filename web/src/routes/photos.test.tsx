import { screen, waitFor } from "@testing-library/react";
import { Photos } from "./photos";
import { fakeApi, renderWithProviders } from "../test-utils";
import { test, expect } from "vitest";

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

test("empty state when no photos", async () => {
  renderWithProviders(<Photos />, {
    client: fakeApi({ "/photos": { items: [], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getByText(/No photos yet/)).toBeInTheDocument());
});

test("renders a tile per photo", async () => {
  renderWithProviders(<Photos />, {
    client: fakeApi({ "/photos": { items: [photo(1), photo(2)], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getAllByRole("link")).toHaveLength(2));
  const first = screen.getAllByRole("link")[0];
  expect(first).toHaveAttribute("href", "/viewer/1?from=all");
  expect(await screen.findByAltText("p1.jpg")).toBeInTheDocument();
});

test("a fetch failure offers a retry", async () => {
  renderWithProviders(<Photos />, {
    client: fakeApi({ "/photos": Object.assign(new Error("down"), { status: 500 }) }),
  });
  expect(await screen.findByRole("alert")).toHaveTextContent(/Cannot reach the server/);
  expect(screen.getByText("Retry")).toBeInTheDocument();
});

test("a photo without a thumbnail shows a placeholder instead of an image", async () => {
  const noThumb = { ...photo(3), thumbnail_key: undefined };
  renderWithProviders(<Photos />, {
    client: fakeApi({ "/photos": { items: [noThumb], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getByText("n/a")).toBeInTheDocument());
  expect(screen.queryByRole("img")).not.toBeInTheDocument();
});

test("a further page keeps the pagination sentinel mounted", async () => {
  renderWithProviders(<Photos />, {
    client: fakeApi({ "/photos": { items: [photo(1)], next_cursor: "c2" } }),
  });
  await waitFor(() => expect(screen.getByTestId("grid-sentinel")).toBeInTheDocument());
});
