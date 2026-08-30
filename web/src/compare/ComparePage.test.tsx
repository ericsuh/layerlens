import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { GOLDEN_GRAPH, GOLDEN_TREE_APP, GOLDEN_TREE_ROOT } from "../fixtures";
import { renderApp } from "../testing";
import { ComparePage } from "./ComparePage";

const LEFT = GOLDEN_GRAPH.left.id;
const RIGHT = GOLDEN_GRAPH.right.id;
const PAIR = `?left=${LEFT}&right=${RIGHT}`;

describe("ComparePage", () => {
  beforeEach(() => {
    // The compare page now mounts the diff tree as well, so the stub has to
    // answer both endpoints; returning the graph for everything would feed a
    // LayerGraph to the tree adapter.
    vi.stubGlobal(
      "fetch",
      vi.fn((input: string | URL) => {
        const url = new URL(input, "http://localhost");
        const body = url.pathname.endsWith("/diff/layers")
          ? GOLDEN_GRAPH
          : (url.searchParams.get("path") ?? "/") === "/"
            ? GOLDEN_TREE_ROOT
            : GOLDEN_TREE_APP;
        return Promise.resolve(
          new Response(JSON.stringify(body), {
            status: 200,
            headers: { "content-type": "application/json" },
          }),
        );
      }),
    );
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("defaults to the full stack of each image when l and r are absent", async () => {
    renderApp(<ComparePage />, { path: `/compare${PAIR}` });
    expect(await screen.findByTestId("selection-chip-a")).toHaveTextContent("A @ layer 8");
    expect(screen.getByTestId("selection-chip-b")).toHaveTextContent("B @ layer 8");
  });

  it("restores the exact selection a pasted link carries", async () => {
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=6&r=8` });
    expect(await screen.findByTestId("selection-chip-a")).toHaveTextContent("A @ layer 6");
    expect(screen.getByTestId("selection-chip-b")).toHaveTextContent("B @ layer 8");
    expect(screen.getByTestId("layer-card-a-6")).toHaveAttribute("aria-checked", "true");
  });

  it("writes a trunk click back to the URL as l === r (the intentional self-diff)", async () => {
    const user = userEvent.setup();
    const { history } = renderApp(<ComparePage />, { path: `/compare${PAIR}` });
    await user.click(await screen.findByTestId("layer-card-trunk-3"));
    await waitFor(() => {
      expect(history.at(-1)).toBe(`/compare${PAIR}&l=3&r=3`);
    });
  });

  it("writes a branch click back as a change to that side only", async () => {
    const user = userEvent.setup();
    const { history } = renderApp(<ComparePage />, { path: `/compare${PAIR}&l=8&r=8` });
    await user.click(await screen.findByTestId("layer-card-b-6"));
    await waitFor(() => {
      expect(history.at(-1)).toBe(`/compare${PAIR}&l=8&r=6`);
    });
  });

  it("carries phase-007's tree params through a selection change untouched", async () => {
    const user = userEvent.setup();
    const { history } = renderApp(<ComparePage />, {
      path: `/compare${PAIR}&path=/app&filter=all`,
    });
    await user.click(await screen.findByTestId("layer-card-a-6"));
    await waitFor(() => {
      const last = history.at(-1) ?? "";
      expect(last).toContain("path=/app");
      expect(last).toContain("filter=all");
    });
  });

  it("names no layer at all until the layer counts have been read", () => {
    vi.stubGlobal("fetch", vi.fn(() => new Promise<Response>(() => undefined)));
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=4&r=7` });
    const panel = screen.getByTestId("fs-skeleton").parentElement;
    expect(panel).not.toHaveTextContent("@ layer");
    expect(panel).not.toHaveTextContent("@ base");
  });

  it("refuses a link that names only one image rather than guessing", () => {
    renderApp(<ComparePage />, { path: `/compare?left=${LEFT}` });
    expect(screen.getByRole("alert")).toHaveTextContent("This comparison link is incomplete");
  });

  it("surfaces the server's message when the pair cannot be loaded", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn(() =>
        Promise.resolve(
          new Response(
            JSON.stringify({ error: { code: "image_not_found", message: "unknown image id" } }),
            { status: 404, headers: { "content-type": "application/json" } },
          ),
        ),
      ),
    );
    renderApp(<ComparePage />, { path: `/compare${PAIR}` });
    expect(await screen.findByRole("alert")).toHaveTextContent("unknown image id");
  });

  it("keeps the filesystem panel's comparison line in step with the selection", async () => {
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=4&r=7` });
    await screen.findByTestId("selection-chip-a");
    expect(screen.getByTestId("fs-compare-a")).toHaveTextContent("A @ layer 4");
    expect(screen.getByTestId("fs-compare-b")).toHaveTextContent("B @ layer 7");
  });
});
