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
  key: "name" | "status" | "size" | "deltaSize" | "files" | "deltaFiles";
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
 * Order matters: `Name | ± | Size | Δ size | Files | Δ files`.
 *
 * Each absolute is followed by its own delta, so the pair reads as one unit
 * ("14.3 MiB, of which +695 B is new") instead of asking the eye to jump two
 * columns to pair them up.
 *
 * There is no separate relative-size column: the bar lives inside the Size
 * cell, which is the number it visualizes. Two columns for one quantity was
 * width spent twice.
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
    // No count: a directory row used to read "± 66", which was read as a size
    // or a file count as often as a descendant tally. The glyph alone says
    // "something below here changed", and the Δ columns say how much.
    title: "Change status: + added, − removed, ± modified or contains changes, · unchanged",
    widthPx: 30,
    worstCase: "±",
  },
  {
    key: "size",
    label: "Size",
    title:
      "Total size in image B, with a bar scaled against the largest top-level entry — hover a row for the A-side totals",
    widthPx: 116,
    worstCase: "1023.9 MiB over a 104px track",
  },
  {
    key: "deltaSize",
    label: "Δ size",
    title: "Change in total size, B relative to A",
    widthPx: 84,
    worstCase: "−1023.9 MiB",
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
    key: "deltaFiles",
    label: "Δ files",
    title: "Change in file count, B relative to A",
    widthPx: 64,
    worstCase: "+999,999",
    hideBelow1280: true,
  },
];

/**
 * The grid template, kept here as the single source of truth and asserted
 * against `.ll-tgrid` by a unit test so the CSS and this table cannot drift.
 */
export const TREE_GRID_TEMPLATE = "minmax(0,1fr) 30px 116px 84px 56px 64px";

/** The px classes each column's cell carries, for the tests that assert them. */
export const NUMERIC_COLUMN_KEYS = ["size", "deltaSize", "files", "deltaFiles"] as const;
