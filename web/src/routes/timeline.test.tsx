import { screen, waitFor, fireEvent } from "@testing-library/react";
import { Timeline } from "./timeline";
import { fakeApi, renderWithProviders } from "../test-utils";
import { test, expect } from "vitest";

const photo = (id: number, taken?: string) => ({
  id,
  album_id: 1,
  filename: `p${id}.jpg`,
  relative_path: `a/p${id}.jpg`,
  mime_type: "image/jpeg",
  thumbnail_key: `k-${id}`,
  taken_at: taken,
  file_size: 1,
  file_mtime_ns: 1,
});

test("renders a heading per year/month", async () => {
  renderWithProviders(<Timeline />, {
    client: fakeApi({
      "/photos/timeline": {
        items: [photo(1, "2025-05-04T10:00:00"), photo(2, "2025-04-30T10:00:00")],
        next_cursor: "",
      },
    }),
  });
  await waitFor(() => expect(screen.getByText("2025 / 05")).toBeInTheDocument());
  expect(screen.getByText("2025 / 04")).toBeInTheDocument();
});

test("undated photos yield the empty state", async () => {
  renderWithProviders(<Timeline />, {
    client: fakeApi({ "/photos/timeline": { items: [photo(1)], next_cursor: "" } }),
  });
  await waitFor(() => expect(screen.getByText(/No dated photos yet/)).toBeInTheDocument());
});

test("load more fetches the next cursor page", async () => {
  const client = fakeApi({
    "/photos/timeline": { items: [photo(1, "2025-05-04T10:00:00")], next_cursor: "c2" },
  });
  renderWithProviders(<Timeline />, { client });
  const button = await screen.findByText("Load more");
  fireEvent.click(button);
  await waitFor(() =>
    expect(client.getJson).toHaveBeenCalledWith("/photos/timeline?cursor=c2"),
  );
});
