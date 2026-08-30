import "@testing-library/jest-dom/vitest";

import { cleanup } from "@testing-library/react";
import { afterEach } from "vitest";

// Testing Library only auto-cleans when a global afterEach exists at import
// time; registering it here keeps every suite isolated.
afterEach(() => {
  cleanup();
});

/**
 * jsdom has no layout: every element reports zero size and there is no
 * `ResizeObserver`. TanStack Virtual reads `offsetWidth`/`offsetHeight` to
 * decide what is on screen, so without a shim the tree renders zero rows and
 * every assertion about it would be vacuous rather than failing loudly.
 *
 * The shim is deliberately narrow: a no-op observer, and a viewport-sized box
 * for the tree's scroll container only. Everything else keeps jsdom's zeros,
 * so nothing else quietly starts believing it has been laid out. Real geometry
 * — column alignment, wrapping, virtualization bounds — is asserted in
 * `e2e/columns.spec.ts` against a real browser, which is the only place those
 * questions have an answer.
 */
class NoopResizeObserver implements ResizeObserver {
  observe(): void {
    /* jsdom never resizes */
  }
  unobserve(): void {
    /* jsdom never resizes */
  }
  disconnect(): void {
    /* jsdom never resizes */
  }
}
globalThis.ResizeObserver = NoopResizeObserver;

const VIRTUAL_VIEWPORT = { width: 900, height: 640 };
for (const [property, size] of [
  ["offsetWidth", VIRTUAL_VIEWPORT.width],
  ["offsetHeight", VIRTUAL_VIEWPORT.height],
] as const) {
  Object.defineProperty(HTMLElement.prototype, property, {
    configurable: true,
    get(this: HTMLElement) {
      return this.dataset.testid === "tree-scroll" ? size : 0;
    },
  });
}
