import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { ApiProvider } from "./context";
import { useAllPhotos, useCategories } from "./queries";
import type { ApiClient } from "./client";
import { test, expect, vi } from "vitest";

function wrap(client: ApiClient) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return ({ children }: { children: ReactNode }) => (
    <QueryClientProvider client={qc}>
      <ApiProvider client={client}>{children}</ApiProvider>
    </QueryClientProvider>
  );
}

function PhotoProbe() {
  const q = useAllPhotos();
  if (q.isLoading) return <div>load</div>;
  return (
    <div>
      <span data-testid="count">{q.data?.pages.flatMap((p) => p.items).length ?? 0}</span>
      <span data-testid="hasNext">{String(q.hasNextPage)}</span>
      <button onClick={() => void q.fetchNextPage()}>more</button>
    </div>
  );
}

test("useAllPhotos aggregates pages", async () => {
  const client: ApiClient = {
    getJson: vi.fn().mockResolvedValueOnce({ items: [{ id: 1 }, { id: 2 }], next_cursor: "" }),
    getBlob: vi.fn(),
  };
  const Wrapper = wrap(client);
  render(
    <Wrapper>
      <PhotoProbe />
    </Wrapper>,
  );
  await waitFor(() => expect(screen.getByTestId("count")).toHaveTextContent("2"));
  expect(screen.getByTestId("hasNext")).toHaveTextContent("false");
});

test('an empty next_cursor ends pagination, a non-empty one continues it', async () => {
  const getJson = vi
    .fn()
    .mockResolvedValueOnce({ items: [{ id: 1 }], next_cursor: "c2" })
    .mockResolvedValueOnce({ items: [{ id: 2 }], next_cursor: "" });
  const client: ApiClient = { getJson, getBlob: vi.fn() };
  const Wrapper = wrap(client);
  render(
    <Wrapper>
      <PhotoProbe />
    </Wrapper>,
  );
  await waitFor(() => expect(screen.getByTestId("hasNext")).toHaveTextContent("true"));
  screen.getByText("more").click();
  await waitFor(() => expect(screen.getByTestId("count")).toHaveTextContent("2"));
  expect(getJson).toHaveBeenLastCalledWith("/photos?cursor=c2");
});

function CategoryProbe() {
  const q = useCategories();
  return <span data-testid="cats">{q.data?.items.length ?? -1}</span>;
}

test("useCategories reads the unpaginated items list", async () => {
  const client: ApiClient = {
    getJson: vi.fn().mockResolvedValue({ items: [{ id: 1, name: "travel" }] }),
    getBlob: vi.fn(),
  };
  const Wrapper = wrap(client);
  render(
    <Wrapper>
      <CategoryProbe />
    </Wrapper>,
  );
  await waitFor(() => expect(screen.getByTestId("cats")).toHaveTextContent("1"));
  expect(client.getJson).toHaveBeenCalledWith("/categories");
});
