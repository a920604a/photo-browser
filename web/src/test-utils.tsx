import { render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { ReactElement, ReactNode } from "react";
import { vi } from "vitest";
import { ApiProvider } from "./api/context";
import { MediaProvider } from "./media/hooks";
import type { ApiClient } from "./api/client";

/** An ApiClient whose getJson answers by path prefix; anything else 404s. */
export function fakeApi(routes: Record<string, unknown>, blob?: Blob): ApiClient {
  return {
    getJson: vi.fn().mockImplementation((path: string) => {
      const key = Object.keys(routes).find((k) => path === k || path.startsWith(k));
      if (key === undefined) {
        return Promise.reject(Object.assign(new Error(`no route: ${path}`), { status: 404 }));
      }
      const v = routes[key];
      return v instanceof Error ? Promise.reject(v) : Promise.resolve(v);
    }),
    getBlob: vi
      .fn()
      .mockResolvedValue(blob ?? new Blob([new Uint8Array([1])], { type: "image/webp" })),
  };
}

export function renderWithProviders(
  ui: ReactElement,
  opts: { client: ApiClient; route?: string } ,
) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const Wrapper = ({ children }: { children: ReactNode }) => (
    <MemoryRouter initialEntries={[opts.route ?? "/"]}>
      <QueryClientProvider client={qc}>
        <ApiProvider client={opts.client}>
          <MediaProvider api={opts.client}>{children}</MediaProvider>
        </ApiProvider>
      </QueryClientProvider>
    </MemoryRouter>
  );
  return render(ui, { wrapper: Wrapper });
}
