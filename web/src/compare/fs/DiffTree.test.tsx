import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { TreePage } from "../../api/types";
import { GOLDEN_GRAPH, GOLDEN_TREE_APP, GOLDEN_TREE_ROOT } from "../../fixtures";
import { renderApp } from "../../testing";
import { ComparePage } from "../ComparePage";

const LEFT = GOLDEN_GRAPH.left.id;
const RIGHT = GOLDEN_GRAPH.right.id;
const PAIR = `?left=${LEFT}&right=${RIGHT}`;

const EMPTY: TreePage = {
  path: "/usr",
  rows: [],
  totalRows: 0,
  maxSiblingBytes: 0,
  pathStatus: "unchanged",
  pathAgg: { leftBytes: 1, rightBytes: 1, leftFiles: 1, rightFiles: 1 },
};

interface StubOptions {
  /** Per-path overrides, keyed by the `path` query parameter. */
  pages?: Record<string, TreePage>;
  onRequest?: (url: URL) => void;
}

function stubFetch(options: StubOptions = {}) {
  const json = (body: unknown) =>
    Promise.resolve(
      new Response(JSON.stringify(body), {
        status: 200,
        headers: { "content-type": "application/json" },
      }),
    );
  vi.stubGlobal(
    "fetch",
    vi.fn((input: string | URL) => {
      const url = new URL(input, "http://localhost");
      options.onRequest?.(url);
      if (url.pathname.endsWith("/diff/layers")) {
        return json(GOLDEN_GRAPH);
      }
      const path = url.searchParams.get("path") ?? "/";
      const override = options.pages?.[path];
      if (override !== undefined) {
        return json(override);
      }
      if (path === "/") {
        return json(GOLDEN_TREE_ROOT);
      }
      if (path === "/app") {
        return json(GOLDEN_TREE_APP);
      }
      return json({ ...EMPTY, path });
    }),
  );
}

describe("DiffTree", () => {
  beforeEach(() => {
    stubFetch();
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it("renders the root listing with the sticky header above it", async () => {
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=7&r=8` });
    expect(await screen.findByTestId("tree-row-/app")).toBeInTheDocument();
    expect(screen.getByTestId("tree-header")).toHaveClass("ll-tgrid");
    expect(screen.getByRole("tree", { name: "Filesystem diff" })).toBeInTheDocument();
    expect(screen.getByTestId("tree-row-/app")).toHaveAttribute("aria-level", "1");
    // `aria-setsize` is the server's post-filter child count of the parent, not
    // the number of rows this client has paged in.
    expect(screen.getByTestId("tree-row-/app")).toHaveAttribute(
      "aria-setsize",
      String(GOLDEN_TREE_ROOT.totalRows),
    );
  });

  it("expands a directory from its embedded depth=2 children, with no request", async () => {
    const paths: string[] = [];
    vi.unstubAllGlobals();
    stubFetch({ onRequest: (url) => paths.push(url.searchParams.get("path") ?? "/") });

    const user = userEvent.setup();
    // A real `gcTime`: the seed is written before anything observes that key,
    // and the suite default of 0 would collect it before the expand reads it.
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=7&r=8`, gcTime: 60_000 });
    const app = await screen.findByTestId("tree-row-/app");
    await user.click(within(app).getByTitle("Expand"));

    expect(await screen.findByTestId("tree-row-/app/debug.log")).toBeInTheDocument();
    // The root came back at depth=2 with every child of /app embedded, so the
    // expand is a render, not a round trip (§8.4).
    expect(paths.filter((path) => path === "/app")).toHaveLength(0);
  });

  it("shows the changed-only empty state with a real hidden count and a way out", async () => {
    vi.unstubAllGlobals();
    stubFetch({
      pages: {
        "/": { ...EMPTY, path: "/", pathAgg: { leftBytes: 5, rightBytes: 9, leftFiles: 1, rightFiles: 2 }, pathStatus: "modified" },
      },
    });
    const user = userEvent.setup();
    const { history } = renderApp(<ComparePage />, { path: `/compare${PAIR}&l=7&r=8` });

    const empty = await screen.findByTestId("tree-empty-filtered");
    expect(empty).toHaveTextContent("No changes in this directory");
    await user.click(within(empty).getByRole("button", { name: "Show all entries" }));
    await waitFor(() => {
      expect(history.at(-1)).toContain("filter=all");
    });
  });

  it("calls the identical-filesystems state only at the real root", async () => {
    vi.unstubAllGlobals();
    stubFetch({ pages: { "/": { ...EMPTY, path: "/" } } });
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=4&r=4` });
    expect(await screen.findByTestId("tree-empty-identical")).toBeInTheDocument();

    // The same all-unchanged shape one level down is state #26, not #25: the
    // comparison as a whole is not identical just because this directory is.
    vi.unstubAllGlobals();
    stubFetch({ pages: { "/usr": EMPTY } });
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=7&r=8&path=/usr` });
    expect(await screen.findByTestId("tree-empty-filtered")).toBeInTheDocument();
  });

  it("filters loaded rows by name and reports when nothing matches", async () => {
    const user = userEvent.setup();
    renderApp(<ComparePage />, { path: `/compare${PAIR}&l=7&r=8` });
    await screen.findByTestId("tree-row-/app");

    await user.type(screen.getByTestId("name-filter"), "zzz-no-such-entry");
    expect(await screen.findByTestId("tree-empty-name")).toHaveTextContent("zzz-no-such-entry");

    await user.clear(screen.getByTestId("name-filter"));
    expect(await screen.findByTestId("tree-row-/app")).toBeInTheDocument();
  });

  it("keeps the drill-down root, the filter and the layer points in the URL", async () => {
    const user = userEvent.setup();
    const { history } = renderApp(<ComparePage />, { path: `/compare${PAIR}&l=7&r=8` });
    const app = await screen.findByTestId("tree-row-/app");

    await user.click(within(app).getByRole("button", { name: "Open /app as root" }));
    await waitFor(() => {
      expect(history.at(-1)).toBe(`/compare${PAIR}&l=7&r=8&path=/app`);
    });

    await user.selectOptions(screen.getByTestId("filter-select"), "all");
    await waitFor(() => {
      expect(history.at(-1)).toBe(`/compare${PAIR}&l=7&r=8&path=/app&filter=all`);
    });
  });
});
