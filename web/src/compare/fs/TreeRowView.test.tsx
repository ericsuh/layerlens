import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";

import type { TreeAgg, TreeRow } from "../../api/types";
import { renderApp } from "../../testing";
import { TreeRowView } from "./TreeRowView";

function agg(overrides: Partial<TreeAgg> = {}): TreeAgg {
  return { leftBytes: 0, rightBytes: 0, leftFiles: 0, rightFiles: 0, ...overrides };
}

function renderRow(row: TreeRow, overrides: Partial<Parameters<typeof TreeRowView>[0]> = {}) {
  const handlers = { onToggle: vi.fn(), onDrill: vi.fn(), onSelect: vi.fn() };
  renderApp(
    <TreeRowView
      row={row}
      level={0}
      expanded={false}
      selected={false}
      maxSiblingBytes={1000}
      itemProps={{ role: "treeitem" }}
      {...handlers}
      {...overrides}
    />,
  );
  return { ...handlers, element: screen.getByTestId(`tree-row-${row.path}`) };
}

const cell = (element: HTMLElement, id: string) => within(element).getByTestId(id);

describe("TreeRowView", () => {
  it("tints an added file, shows the + glyph and a signed delta", () => {
    const { element } = renderRow({
      name: "debug.log",
      path: "/app/debug.log",
      status: "added",
      right: { kind: "file", mode: 0o644, sizeBytes: 161792 },
      agg: agg({ rightBytes: 161792, rightFiles: 1, addedBytes: 161792, addedFiles: 1 }),
      hasChildren: false,
      childCount: 0,
    });
    expect(element).toHaveAttribute("data-status", "added");
    expect(element.className).toContain("ll-trow-added");
    expect(cell(element, "cell-delta-size")).toHaveTextContent("+158 KiB");
    expect(cell(element, "cell-size")).toHaveTextContent("158 KiB");
    // A file is not a subtree: its own addition is the glyph, not a "+1".
    expect(cell(element, "cell-delta-files")).toHaveTextContent("");
    expect(cell(element, "cell-files")).toHaveTextContent("");
    expect(element).toHaveAttribute("aria-label", expect.stringContaining("added"));
  });

  it("strikes a removed row through and shows its A-side values", () => {
    const { element } = renderRow({
      name: "apt",
      path: "/var/lib/apt",
      status: "removed",
      left: { kind: "dir", mode: 0o755, sizeBytes: 0 },
      agg: agg({ leftBytes: 1249280, leftFiles: 3, removedBytes: 1249280, removedFiles: 3 }),
      hasChildren: true,
      childCount: 3,
    });
    expect(element.className).toContain("ll-trow-removed");
    // The B side is gone, so the absolute columns quote A — struck through, so
    // "1.2 MiB" cannot be misread as "still there".
    expect(cell(element, "cell-size")).toHaveTextContent("1.2 MiB");
    expect(cell(element, "cell-size").className).toContain("ll-tnum-gone");
    expect(cell(element, "cell-files")).toHaveTextContent("3");
    expect(cell(element, "cell-delta-size")).toHaveTextContent("−1.2 MiB");
  });

  it("gives a merely-containing directory a ± N summary and no tint", () => {
    const { element } = renderRow({
      name: "app",
      path: "/app",
      status: "modified",
      left: { kind: "dir", mode: 0o755, sizeBytes: 0 },
      right: { kind: "dir", mode: 0o755, sizeBytes: 0 },
      agg: agg({
        leftBytes: 8973541,
        rightBytes: 13966777,
        leftFiles: 249,
        rightFiles: 307,
        addedFiles: 61,
        removedFiles: 3,
        modifiedFiles: 2,
      }),
      hasChildren: true,
      childCount: 5,
    });
    expect(element).toHaveAttribute("data-status", "contains");
    // No full-row tint: this directory did not change, its contents did.
    expect(element.className).not.toContain("ll-trow-added");
    expect(element.className).not.toContain("ll-trow-removed");
    expect(within(element).getByTitle("66 changed descendants")).toHaveTextContent("± 66");
    expect(cell(element, "cell-delta-files")).toHaveTextContent("+58");
    expect(cell(element, "cell-size")).toHaveAttribute(
      "title",
      "A: 8.6 MiB (249 files) → B: 13.3 MiB (307 files)",
    );
  });

  it("mutes an unchanged row and renders an em dash for a zero delta", () => {
    const { element } = renderRow({
      name: "package.json",
      path: "/app/package.json",
      status: "unchanged",
      left: { kind: "file", mode: 0o644, sizeBytes: 1536 },
      right: { kind: "file", mode: 0o644, sizeBytes: 1536 },
      agg: agg({ leftBytes: 1536, rightBytes: 1536, leftFiles: 1, rightFiles: 1 }),
      hasChildren: false,
      childCount: 0,
    });
    expect(element.className).toContain("ll-trow-unchanged");
    expect(cell(element, "cell-delta-size")).toHaveTextContent("—");
    expect(cell(element, "cell-delta-size").className).toContain("ll-tnum-zero");
  });

  it("keeps chevron, name and drill-down as three separate affordances", async () => {
    const user = userEvent.setup();
    const { element, onToggle, onDrill, onSelect } = renderRow({
      name: "src",
      path: "/app/src",
      status: "modified",
      left: { kind: "dir", mode: 0o755, sizeBytes: 0 },
      right: { kind: "dir", mode: 0o755, sizeBytes: 0 },
      agg: agg({ leftBytes: 25119, rightBytes: 20988, leftFiles: 7, rightFiles: 4, removedFiles: 3 }),
      hasChildren: true,
      childCount: 3,
    });

    await user.click(within(element).getByTitle("Expand"));
    expect(onToggle).toHaveBeenCalledTimes(1);
    // On a directory the name toggles too; drill-down is its own button.
    await user.click(within(element).getByTitle(/^\/app\/src —/));
    expect(onToggle).toHaveBeenCalledTimes(2);
    await user.click(within(element).getByRole("button", { name: "Open /app/src as root" }));
    expect(onDrill).toHaveBeenCalledTimes(1);
    expect(onSelect).not.toHaveBeenCalled();
  });

  it("selects rather than toggles when the row is a file", async () => {
    const user = userEvent.setup();
    const { element, onToggle, onSelect } = renderRow({
      name: "main.js",
      path: "/app/main.js",
      status: "modified",
      left: { kind: "file", mode: 0o644, sizeBytes: 4212 },
      right: { kind: "file", mode: 0o644, sizeBytes: 4907 },
      agg: agg({ leftBytes: 4212, rightBytes: 4907, leftFiles: 1, rightFiles: 1, modifiedFiles: 1 }),
      hasChildren: false,
      childCount: 0,
    });
    await user.click(within(element).getByTitle(/^\/app\/main\.js —/));
    expect(onSelect).toHaveBeenCalledTimes(1);
    expect(onToggle).not.toHaveBeenCalled();
  });

  it("draws one indent guide per level and never puts numbers outside their track", () => {
    const { element } = renderRow(
      {
        name: "index.js",
        path: "/a/b/c/index.js",
        status: "unchanged",
        left: { kind: "file", mode: 0o644, sizeBytes: 10 },
        right: { kind: "file", mode: 0o644, sizeBytes: 10 },
        agg: agg({ leftBytes: 10, rightBytes: 10, leftFiles: 1, rightFiles: 1 }),
        hasChildren: false,
        childCount: 0,
      },
      { level: 6 },
    );
    // Indentation lives inside the Name cell — that is what keeps the header
    // aligned with rows at every depth (RESEARCH Q12 fix 1).
    const nameCell = element.querySelector(".ll-tcell-name");
    expect(nameCell?.querySelectorAll(".ll-tguide")).toHaveLength(6);
    expect(element.querySelectorAll(":scope > div")).toHaveLength(7);
    for (const id of ["cell-delta-size", "cell-delta-files", "cell-size", "cell-files"]) {
      expect(cell(element, id).className).toContain("ll-tnum");
    }
  });
});
