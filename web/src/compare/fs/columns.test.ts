import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import { NUMERIC_COLUMN_KEYS, TREE_COLUMNS, TREE_GRID_TEMPLATE } from "./columns";

/**
 * The header and the rows share one CSS class, so alignment cannot drift at
 * runtime. What *can* drift is this table and that class — so the drift is
 * checked here, at build time, rather than left to a screenshot to notice.
 */
const CSS = readFileSync(
  resolve(dirname(fileURLToPath(import.meta.url)), "../../app.css"),
  "utf8",
);

function templateFromColumns(): string {
  return TREE_COLUMNS.map((column) =>
    column.widthPx === "fluid" ? "minmax(0,1fr)" : `${String(column.widthPx)}px`,
  ).join(" ");
}

describe("tree column definition", () => {
  it("matches the grid template it documents", () => {
    expect(templateFromColumns()).toBe(TREE_GRID_TEMPLATE);
  });

  it("matches the `.ll-tgrid` rule the header and rows actually apply", () => {
    const rule = /\.ll-tgrid\s*\{[^}]*grid-template-columns:\s*([^;]+);/.exec(CSS);
    expect(rule).not.toBeNull();
    const declared = (rule?.[1] ?? "").replace(/\s+/g, " ").replace("minmax(0, 1fr)", "minmax(0,1fr)");
    expect(declared).toBe(TREE_GRID_TEMPLATE);
  });

  it("drops exactly the two file-count columns below 1280px, in one rule", () => {
    // The header cells and the row cells both carry `ll-tcol-optional`, so the
    // responsive rule can never hide one and not the other.
    expect(CSS).toMatch(/@media \(max-width: 1279px\)[\s\S]*?\.ll-tcol-optional\s*\{\s*display: none;/);
    const optional = TREE_COLUMNS.filter((column) => column.hideBelow1280 === true).map(
      (column) => column.key,
    );
    expect(optional).toEqual(["files", "deltaFiles"]);
  });

  it("labels every column and explains it in a title", () => {
    // Each absolute is immediately followed by its own delta, and there is no
    // separate relative-size column — the bar lives in the Size cell.
    expect(TREE_COLUMNS.map((column) => column.label)).toEqual([
      "Name",
      "±",
      "Size",
      "Δ size",
      "Files",
      "Δ files",
    ]);
    for (const column of TREE_COLUMNS) {
      expect(column.title.length).toBeGreaterThan(10);
    }
  });

  it("reserves the worst case DESIGN §3 names, not the fixture's data", () => {
    const widths = Object.fromEntries(TREE_COLUMNS.map((c) => [c.key, c.widthPx]));
    expect(widths).toMatchObject({
      status: 30,
      size: 116,
      deltaSize: 84,
      files: 56,
      deltaFiles: 64,
    });
    // The bar is not its own column any more, and must not creep back into one.
    expect(TREE_COLUMNS.some((column) => column.key === ("bar" as string))).toBe(false);
    for (const key of NUMERIC_COLUMN_KEYS) {
      expect(TREE_COLUMNS.find((column) => column.key === key)?.worstCase).toBeTruthy();
    }
  });

  it("keeps numeric cells on one line by class contract", () => {
    // The rendered-browser proof is `e2e/columns.spec.ts` fix 2; this is the
    // cheap guard that the class carrying it never loses the declaration.
    expect(CSS).toMatch(/\.ll-tnum\s*\{[^}]*white-space:\s*nowrap;/);
    expect(CSS).toMatch(/\.ll-tnum\s*\{[^}]*text-align:\s*right;/);
    expect(CSS).toMatch(/\.ll-tnum\s*\{[^}]*font-variant-numeric:\s*tabular-nums;/);
  });
});
