/**
 * The one column grid the header row and every tree row share.
 *
 * This module exists so the two can never drift: the template lives in the
 * `.ll-tgrid` class (`app.css`), which both the header and the rows apply, and
 * the labels/tooltips/widths live here, where the header renders them and the
 * tests assert them. Nothing depth-dependent may live outside the Name cell —
 * indent guides and the chevron are *inside* it — which is what makes the
 * header stay aligned with rows at every indent level by construction
 * (DESIGN §5.3, RESEARCH Q12 fix 1).
 */

export interface TreeColumn {
  key: "name" | "status" | "deltaSize" | "deltaFiles" | "size" | "files" | "bar";
  label: string;
  /** The `title` tooltip that carries the meaning the short label cannot. */
  title: string;
  /** Reserved track width in px; the Name column is fluid. */
  widthPx: number | "fluid";
  /** The worst-case rendering the width was sized for (DESIGN §3). */
  worstCase: string;
  /** Hidden below 1280px, data preserved in the row tooltip (DESIGN §8). */
  hideBelow1280?: boolean;
}

/**
 * Order matters: `Name | ± | Δ size | Δ files | Size | Files | Rel. size`.
 * The Δ pair sits directly after the status glyph because "what changed" is
 * the tool's primary question; the absolute pair is the "after" context; the
 * bar stays outermost as a purely visual summary.
 */
export const TREE_COLUMNS: readonly TreeColumn[] = [
  {
    key: "name",
    label: "Name",
    title: "Entry name — directories first, then files, each name-ascending",
    widthPx: "fluid",
    worstCase: "(truncates)",
  },
  {
    key: "status",
    label: "±",
    title:
      "Change status: + added, − removed, ± modified, · unchanged. On a directory, ± N counts changed descendants.",
    widthPx: 42,
    worstCase: "± 9.9M",
  },
  {
    key: "deltaSize",
    label: "Δ size",
    title: "Change in total size, B relative to A",
    widthPx: 84,
    worstCase: "−1023.9 MiB",
  },
  {
    key: "deltaFiles",
    label: "Δ files",
    title: "Change in file count, B relative to A",
    widthPx: 64,
    worstCase: "+999,999",
    hideBelow1280: true,
  },
  {
    key: "size",
    label: "Size",
    title: "Total size in image B — hover a row for the A-side totals",
    widthPx: 76,
    worstCase: "1023.9 MiB",
  },
  {
    key: "files",
    label: "Files",
    title: "Total file count in image B — hover a row for the A-side totals",
    widthPx: 56,
    worstCase: "999,999",
    hideBelow1280: true,
  },
  {
    key: "bar",
    label: "Rel. size",
    title:
      "Size relative to the largest sibling; hatched segments are the added / removed / modified portions",
    widthPx: 108,
    worstCase: "96px track",
  },
];

/**
 * The grid template, kept here as the single source of truth and asserted
 * against `.ll-tgrid` by a unit test so the CSS and this table cannot drift.
 */
export const TREE_GRID_TEMPLATE = "minmax(0,1fr) 42px 84px 64px 76px 56px 108px";

/** The px classes each column's cell carries, for the tests that assert them. */
export const NUMERIC_COLUMN_KEYS = ["deltaSize", "deltaFiles", "size", "files"] as const;
