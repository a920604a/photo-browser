import "@testing-library/jest-dom/vitest";
import { vi } from "vitest";

// jsdom ships neither of these; the grid's lazy pagination and the blob cache
// both need them present to render at all.
if (!("IntersectionObserver" in globalThis)) {
  class StubIntersectionObserver implements IntersectionObserver {
    readonly root = null;
    readonly rootMargin = "";
    readonly thresholds: ReadonlyArray<number> = [];
    observe() {}
    unobserve() {}
    disconnect() {}
    takeRecords(): IntersectionObserverEntry[] {
      return [];
    }
  }
  globalThis.IntersectionObserver =
    StubIntersectionObserver as unknown as typeof IntersectionObserver;
}

if (!globalThis.URL.createObjectURL) {
  globalThis.URL.createObjectURL = vi.fn(() => "blob://stub");
  globalThis.URL.revokeObjectURL = vi.fn();
}
