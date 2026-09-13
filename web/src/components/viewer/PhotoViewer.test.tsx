import { render, screen, fireEvent, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { MediaProvider } from "../../media/hooks";
import { PhotoViewer } from "./PhotoViewer";
import type { ApiClient } from "../../api/client";
import type { Photo } from "../../api/types";
import { test, expect, vi } from "vitest";

const p = (id: number): Photo => ({
  id,
  album_id: 1,
  filename: `f${id}.jpg`,
  relative_path: "",
  mime_type: "image/jpeg",
  file_size: 1,
  file_mtime_ns: 1,
});

function okClient(): ApiClient {
  return {
    getJson: vi.fn(),
    getBlob: vi.fn().mockResolvedValue(new Blob([new Uint8Array([1])], { type: "image/jpeg" })),
  };
}

/** Renders the router's current URL so navigation is observable. */
function LocationProbe() {
  const loc = useLocation();
  return <span data-testid="loc">{loc.pathname + loc.search}</span>;
}

function renderViewer(node: React.ReactNode, client: ApiClient = okClient()) {
  return render(
    <MemoryRouter initialEntries={["/viewer/2?from=all"]}>
      <MediaProvider api={client}>
        {node}
        <LocationProbe />
      </MediaProvider>
    </MemoryRouter>,
  );
}

test("shows filename and next/prev buttons", () => {
  renderViewer(<PhotoViewer current={p(2)} siblings={[p(1), p(2), p(3)]} from="all" />);
  expect(screen.getByText("f2.jpg")).toBeInTheDocument();
  expect(screen.getByLabelText("Previous")).toHaveAttribute("href", "/viewer/1?from=all");
  expect(screen.getByLabelText("Next")).toHaveAttribute("href", "/viewer/3?from=all");
});

test("the ends of a collection hide the corresponding arrow", () => {
  renderViewer(<PhotoViewer current={p(1)} siblings={[p(1), p(2)]} from="all" />);
  expect(screen.queryByLabelText("Previous")).not.toBeInTheDocument();
  expect(screen.getByLabelText("Next")).toBeInTheDocument();
});

test("fetches the original and renders it", async () => {
  const client = okClient();
  renderViewer(<PhotoViewer current={p(2)} siblings={[p(2)]} from="all" />, client);
  await waitFor(() => expect(screen.getByAltText("f2.jpg")).toBeInTheDocument());
  expect(client.getBlob).toHaveBeenCalledWith("/photos/2/original");
});

test("an unavailable original says so", async () => {
  const client: ApiClient = {
    getJson: vi.fn(),
    getBlob: vi.fn().mockRejectedValue(Object.assign(new Error("nf"), { status: 404 })),
  };
  renderViewer(<PhotoViewer current={p(2)} siblings={[p(2)]} from="all" />, client);
  expect(await screen.findByText("Photo unavailable")).toBeInTheDocument();
});

test("arrow keys navigate to the neighbouring photo", () => {
  renderViewer(<PhotoViewer current={p(2)} siblings={[p(1), p(2), p(3)]} from="all" />);
  fireEvent.keyDown(window, { key: "ArrowRight" });
  expect(screen.getByTestId("loc")).toHaveTextContent("/viewer/3?from=all");
  fireEvent.keyDown(window, { key: "ArrowLeft" });
  expect(screen.getByTestId("loc")).toHaveTextContent("/viewer/1?from=all");
});

test("arrow keys do nothing at the ends of the collection", () => {
  renderViewer(<PhotoViewer current={p(1)} siblings={[p(1)]} from="all" />);
  fireEvent.keyDown(window, { key: "ArrowLeft" });
  fireEvent.keyDown(window, { key: "ArrowRight" });
  expect(screen.getByTestId("loc")).toHaveTextContent("/viewer/2?from=all");
});

test("Escape closes without throwing", () => {
  renderViewer(<PhotoViewer current={p(1)} siblings={[p(1)]} from="all" />);
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.getByRole("dialog")).toBeInTheDocument();
});
