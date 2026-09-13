import { screen, waitFor } from "@testing-library/react";
import { Routes, Route } from "react-router-dom";
import { Categories } from "./categories";
import { CategoryDetail } from "./category-detail";
import { fakeApi, renderWithProviders } from "../test-utils";
import { test, expect } from "vitest";

const cat = (id: number, name: string) => ({ id, name, relative_path: name });
const album = (id: number) => ({
  id,
  category_id: 1,
  name: `album-${id}`,
  relative_path: `travel/album-${id}`,
  cover_photo_id: id * 10,
  cover_thumbnail_key: `k-${id}`,
});

test("lists categories linking to their detail route", async () => {
  renderWithProviders(<Categories />, {
    client: fakeApi({ "/categories": { items: [cat(1, "travel"), cat(2, "family")] } }),
  });
  await waitFor(() => expect(screen.getByText("travel")).toBeInTheDocument());
  expect(screen.getAllByRole("link")[0]).toHaveAttribute("href", "/categories/1");
});

test("no categories yields the empty state", async () => {
  renderWithProviders(<Categories />, { client: fakeApi({ "/categories": { items: [] } }) });
  await waitFor(() => expect(screen.getByText(/No categories yet/)).toBeInTheDocument());
});

function renderDetail(client: ReturnType<typeof fakeApi>) {
  return renderWithProviders(
    <Routes>
      <Route path="/categories/:categoryId" element={<CategoryDetail />} />
    </Routes>,
    { client, route: "/categories/1" },
  );
}

test("category detail titles itself with the category name", async () => {
  renderDetail(
    fakeApi({
      "/categories/1/albums": { items: [album(1)] },
      "/categories": { items: [cat(1, "travel")] },
    }),
  );
  await waitFor(() => expect(screen.getByRole("heading")).toHaveTextContent("travel"));
  expect(await screen.findByText("album-1")).toBeInTheDocument();
});

test("an empty category says so", async () => {
  renderDetail(
    fakeApi({ "/categories/1/albums": { items: [] }, "/categories": { items: [cat(1, "travel")] } }),
  );
  await waitFor(() => expect(screen.getByText(/No albums in this category/)).toBeInTheDocument());
});
