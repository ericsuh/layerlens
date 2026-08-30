/**
 * The pure half of the filesystem diff tree.
 *
 * Everything here is a function of wire data plus UI state — no React, no
 * fetching — so the tree's real logic (page merging, filtering, flattening,
 * bar math, breadcrumbs) is unit-testable without a DOM. `DiffTree.tsx` owns
 * the React and headless-tree wiring and calls into this module.
 */

import type { TreeAgg, TreePage, TreeRow, TreeStatus } from "../../api/types";
import type { TreeFilter } from "../../lib/urlstate";

/** Fixed row height — virtualization needs it up front (DESIGN §2.2). */
export const ROW_HEIGHT = 32;

/** The relative-size bar's track, in px (DESIGN §5.3). */
export const BAR_TRACK_PX = 104;

/** Bars below this clamp up, so "small but non-zero" never renders as nothing. */
export const BAR_MIN_PX = 2;

/**
 * The seven change breakdowns with absent normalized to 0.
 *
 * The server omits them when zero (§6.5), so reading `agg.addedBytes`
 * directly yields `undefined` for the overwhelming majority of rows. Every
 * consumer in this file goes through here instead.
 */
export interface Change {
  addedBytes: number;
  removedBytes: number;
  modifiedBytesLeft: number;
  modifiedBytesRight: number;
  addedFiles: number;
  removedFiles: number;
  modifiedFiles: number;
}

export function changeOf(agg: TreeAgg): Change {
  return {
    addedBytes: agg.addedBytes ?? 0,
    removedBytes: agg.removedBytes ?? 0,
    modifiedBytesLeft: agg.modifiedBytesLeft ?? 0,
    modifiedBytesRight: agg.modifiedBytesRight ?? 0,
    addedFiles: agg.addedFiles ?? 0,
    removedFiles: agg.removedFiles ?? 0,
    modifiedFiles: agg.modifiedFiles ?? 0,
  };
}

/** How many files under this row changed — the `± N` directory summary. */
export function changedDescendants(agg: TreeAgg): number {
  const c = changeOf(agg);
  return c.addedFiles + c.removedFiles + c.modifiedFiles;
}

/** A row is a directory if either side says so — used for the trailing "/". */
export function isDirRow(row: TreeRow): boolean {
  return row.left?.kind === "dir" || row.right?.kind === "dir";
}

/**
 * Only `hasChildren` earns a disclosure triangle.
 *
 * It is deliberately not the same question as `isDirRow`: an empty directory
 * is a directory with nothing to disclose, and by the dir↔file exception
 * (§6.5) a path that is a directory on one side and a file on the other is a
 * *leaf* — a triangle there would expand nothing.
 */
export function isExpandable(row: TreeRow): boolean {
  return row.hasChildren;
}

/** True for the §6.5 dir↔file exception, which the row labels explicitly. */
export function isTypeChange(row: TreeRow): boolean {
  if (row.left === undefined || row.right === undefined) {
    return false;
  }
  return row.left.kind !== row.right.kind;
}

/**
 * The five visual row states of DESIGN §5.3, which are *not* the four wire
 * statuses: a directory whose own existence did not change but whose subtree
 * did renders as "contains" (no tint, `± N` summary), and that distinction is
 * the whole point of the row treatment table.
 */
export type RowKind = "added" | "removed" | "modified" | "contains" | "unchanged";

export function rowKindOf(row: TreeRow): RowKind {
  if (row.status !== "modified") {
    return row.status;
  }
  // A modified directory with changed descendants is a container of changes.
  // A modified directory with none changed itself (a mode change, or the
  // dir↔file exception) and gets the `±` glyph like a file.
  if (isDirRow(row) && changedDescendants(row.agg) > 0) {
    return "contains";
  }
  return "modified";
}

/** Signed byte delta, B relative to A. */
export function byteDelta(agg: TreeAgg): number {
  return agg.rightBytes - agg.leftBytes;
}

/** Signed file-count delta, B relative to A. */
export function fileDelta(agg: TreeAgg): number {
  return agg.rightFiles - agg.leftFiles;
}

// ---------------------------------------------------------------- pages

/** One directory's merged pages. */
export interface DirectoryData {
  rows: TreeRow[];
  totalRows: number;
  maxSiblingBytes: number;
  pathStatus: TreeStatus;
  pathAgg: TreeAgg;
}

const EMPTY_AGG: TreeAgg = { leftBytes: 0, rightBytes: 0, leftFiles: 0, rightFiles: 0 };

/**
 * Appends pages in arrival order, dropping any row path already seen.
 *
 * Duplicates are possible in principle — a resumed cursor lands on whatever
 * now occupies its position — and a duplicated React key is a rendering bug
 * rather than a cosmetic one, so the guard is here and not left to chance.
 * `totalRows` and `maxSiblingBytes` come from the first page: the contract
 * (§6.5) says both are page-stable, and picking one page makes that
 * assumption visible instead of letting later pages silently rescale the bars.
 */
export function mergePages(pages: readonly TreePage[]): DirectoryData {
  const first = pages[0];
  if (first === undefined) {
    return {
      rows: [],
      totalRows: 0,
      maxSiblingBytes: 0,
      pathStatus: "unchanged",
      pathAgg: EMPTY_AGG,
    };
  }
  const rows: TreeRow[] = [];
  const seen = new Set<string>();
  for (const page of pages) {
    for (const row of page.rows) {
      if (seen.has(row.path)) {
        continue;
      }
      seen.add(row.path);
      rows.push(row);
    }
  }
  return {
    rows,
    totalRows: first.totalRows,
    maxSiblingBytes: first.maxSiblingBytes,
    pathStatus: first.pathStatus,
    pathAgg: first.pathAgg,
  };
}

/**
 * Turns a depth=2 row's embedded children into a first page for that
 * directory's own query, so expanding it costs no request (§8.4 step 4).
 *
 * Returns null when the row's children were truncated: the embedded set is
 * then a prefix, and seeding it would leave the directory permanently short
 * of rows with no cursor to continue from. Those directories get their own
 * request on expand, which is exactly what `childrenTruncated` is for.
 */
export function seedPageFromRow(row: TreeRow): TreePage | null {
  if (row.childrenTruncated === true || row.children === undefined) {
    return null;
  }
  let maxSiblingBytes = 0;
  for (const child of row.children) {
    const total = child.agg.leftBytes + child.agg.rightBytes;
    if (total > maxSiblingBytes) {
      maxSiblingBytes = total;
    }
  }
  return {
    path: row.path,
    rows: row.children,
    totalRows: row.childCount,
    maxSiblingBytes,
    pathStatus: row.status,
    pathAgg: row.agg,
  };
}

// ---------------------------------------------------------------- filtering

/**
 * The client-side refinements. `all` and `changed` are the server's own
 * filters and are already applied to what arrived; the three polarity
 * refinements narrow that set without a second request (see the phase file's
 * note on the missing search endpoint).
 */
export function matchesRefinement(row: TreeRow, filter: TreeFilter): boolean {
  const c = changeOf(row.agg);
  switch (filter) {
    case "all":
    case "changed":
      return true;
    case "added":
      return row.status === "added" || c.addedFiles > 0;
    case "removed":
      return row.status === "removed" || c.removedFiles > 0;
    case "modified":
      return row.status === "modified" || c.modifiedFiles > 0;
  }
}

/** Case-insensitive substring match on the entry's own name. */
export function matchesName(row: TreeRow, needle: string): boolean {
  return needle === "" || row.name.toLowerCase().includes(needle);
}

/** The loaded children of a directory, or an empty list when it has none yet. */
export type LoadedRows = (path: string) => readonly TreeRow[] | undefined;

/**
 * True when this row, or any *loaded* descendant, matches the name filter.
 *
 * "Loaded" is the honest qualifier: the name filter searches the window the
 * client has, not the whole image, because §6 defines no search endpoint
 * (DECISIONS, phase 007 delta). Descending only through loaded rows also
 * bounds this walk by what is already in memory.
 */
export function subtreeMatchesName(row: TreeRow, needle: string, loaded: LoadedRows): boolean {
  if (needle === "") {
    return true;
  }
  if (matchesName(row, needle)) {
    return true;
  }
  const seen = new Set<string>();
  const queue: TreeRow[] = [...(loaded(row.path) ?? [])];
  while (queue.length > 0) {
    const next = queue.pop();
    if (next === undefined || seen.has(next.path)) {
      continue;
    }
    seen.add(next.path);
    if (matchesName(next, needle)) {
      return true;
    }
    for (const child of loaded(next.path) ?? []) {
      queue.push(child);
    }
  }
  return false;
}

export interface VisibilityOptions {
  filter: TreeFilter;
  /** Already lower-cased and trimmed by the caller. */
  needle: string;
  loaded: LoadedRows;
}

/** The children of `path` that survive the client-side refinement + name filter. */
export function visibleChildren(path: string, options: VisibilityOptions): TreeRow[] {
  const rows = options.loaded(path);
  if (rows === undefined) {
    return [];
  }
  return rows.filter(
    (row) =>
      matchesRefinement(row, options.filter) &&
      subtreeMatchesName(row, options.needle, options.loaded),
  );
}

/**
 * Directories that must be force-expanded so a name-filter match is reachable
 * (DESIGN §5.3: "matches auto-expand ancestor chain"). Only directories whose
 * own name does not match are expanded — a directory that *is* the match stays
 * closed, because the user searched for it, not for its contents.
 */
export function autoExpandedForName(
  rootPath: string,
  options: VisibilityOptions,
  limit = 5000,
): Set<string> {
  const expanded = new Set<string>();
  if (options.needle === "") {
    return expanded;
  }
  const queue: string[] = [rootPath];
  let budget = limit;
  while (queue.length > 0 && budget > 0) {
    const path = queue.pop();
    if (path === undefined) {
      continue;
    }
    for (const row of options.loaded(path) ?? []) {
      budget -= 1;
      if (!isExpandable(row) || matchesName(row, options.needle)) {
        continue;
      }
      if (subtreeMatchesName(row, options.needle, options.loaded)) {
        expanded.add(row.path);
        queue.push(row.path);
      }
    }
  }
  return expanded;
}

// ---------------------------------------------------------------- flattening

/** The trailing row a directory needs after its last child, if any. */
export type TrailerVariant = "loading" | "error" | "more" | "empty";

/** The minimum a flattened item must carry — headless-tree's `ItemMeta` fits. */
export interface FlatItemMeta {
  itemId: string;
  level: number;
}

export type VisibleEntry =
  | { kind: "item"; id: string; itemIndex: number; level: number }
  | { kind: "trailer"; id: string; dirPath: string; level: number; variant: TrailerVariant };

/**
 * Splices each directory's trailing row (skeletons, error + retry, "Show N
 * more…", "empty") into the flat visible list at the point the directory
 * closes.
 *
 * Implemented with an explicit open-directory stack rather than by walking
 * parent links: the flat list is already in DFS order with levels, so a
 * directory closes exactly when the next item's level is no deeper than its
 * own — which is also true of the last item in the list, and of the root.
 */
export function interleaveTrailers(
  items: readonly FlatItemMeta[],
  options: {
    rootPath: string;
    isExpandedDir: (itemId: string) => boolean;
    trailerFor: (dirPath: string) => TrailerVariant | null;
  },
): VisibleEntry[] {
  const out: VisibleEntry[] = [];
  const stack: { path: string; level: number }[] = [];

  const close = (entry: { path: string; level: number }): void => {
    const variant = options.trailerFor(entry.path);
    if (variant !== null) {
      out.push({
        kind: "trailer",
        id: `trailer:${entry.path}:${variant}`,
        dirPath: entry.path,
        level: entry.level + 1,
        variant,
      });
    }
  };

  items.forEach((item, index) => {
    while (stack.length > 0 && (stack[stack.length - 1]?.level ?? -1) >= item.level) {
      const closed = stack.pop();
      if (closed !== undefined) {
        close(closed);
      }
    }
    out.push({ kind: "item", id: item.itemId, itemIndex: index, level: item.level });
    if (options.isExpandedDir(item.itemId)) {
      stack.push({ path: item.itemId, level: item.level });
    }
  });

  while (stack.length > 0) {
    const closed = stack.pop();
    if (closed !== undefined) {
      close(closed);
    }
  }
  close({ path: options.rootPath, level: -1 });
  return out;
}

// ---------------------------------------------------------------- size bars

export type BarSegmentKind = "unchanged" | "modified" | "added" | "removed";

export interface BarSegment {
  kind: BarSegmentKind;
  /** Share of the bar's own width, 0..1. Segments sum to 1. */
  ratio: number;
}

export interface BarModel {
  widthPx: number;
  segments: BarSegment[];
}

/**
 * Width relative to `scaleBytes` — the largest entry at the top level of the
 * visible tree — split into hatched change segments (DESIGN §5.3,
 * ARCHITECTURE §8.4 step 5).
 *
 * One denominator for the whole tree, not one per directory: a child's bytes
 * are always a subset of its parent's, so a shared denominator guarantees a
 * child's bar is never longer than its parent's. The numerator matches the
 * server's own — `leftBytes + rightBytes`, both sides — so a top-level row can
 * never exceed the track. A zero denominator (a tree of empty files) yields no
 * bar rather than a division by zero.
 */
export function sizeBarModel(
  agg: TreeAgg,
  scaleBytes: number,
  trackPx: number = BAR_TRACK_PX,
): BarModel {
  const total = agg.leftBytes + agg.rightBytes;
  if (total <= 0 || scaleBytes <= 0) {
    return { widthPx: 0, segments: [] };
  }
  const raw = (total / scaleBytes) * trackPx;
  const widthPx = Math.min(trackPx, Math.max(BAR_MIN_PX, Math.round(raw)));

  const c = changeOf(agg);
  const modified = c.modifiedBytesLeft + c.modifiedBytesRight;
  // Whatever is not accounted for by a change is unchanged — and it is
  // clamped, because a server that ever over-reports a breakdown should
  // shorten the neutral segment, never produce a negative width.
  const unchanged = Math.max(0, total - c.addedBytes - c.removedBytes - modified);
  const parts: [BarSegmentKind, number][] = [
    ["unchanged", unchanged],
    ["modified", modified],
    ["added", c.addedBytes],
    ["removed", c.removedBytes],
  ];
  const sum = parts.reduce((acc, [, value]) => acc + value, 0);
  if (sum <= 0) {
    return { widthPx, segments: [{ kind: "unchanged", ratio: 1 }] };
  }
  return {
    widthPx,
    segments: parts
      .filter(([, value]) => value > 0)
      .map(([kind, value]) => ({ kind, ratio: value / sum })),
  };
}

// ---------------------------------------------------------------- breadcrumbs

export interface Crumb {
  label: string;
  path: string;
}

export interface BreadcrumbTrail {
  /** Root first, then the visible segments. */
  crumbs: Crumb[];
  /**
   * The middle crumbs the trail dropped. When non-empty they belong *after*
   * `crumbs[1]` — the root and the first segment always stay — and the UI
   * renders them behind a `…` menu (DESIGN §3).
   */
  hidden: Crumb[];
}

/**
 * Root + segments, collapsing the middle when the path is deep.
 *
 * `maxSegments` counts path segments, not crumbs: at or below it every
 * segment is shown; above it the trail keeps the first segment and the last
 * two, which are the ones that locate the user ("where I started" and "where
 * I am").
 */
export function breadcrumbTrail(path: string, maxSegments = 4): BreadcrumbTrail {
  const segments = path.split("/").filter((segment) => segment !== "");
  const root: Crumb = { label: "/", path: "/" };
  const crumbs: Crumb[] = [];
  let accumulated = "";
  for (const segment of segments) {
    accumulated += `/${segment}`;
    crumbs.push({ label: segment, path: accumulated });
  }
  if (crumbs.length <= maxSegments) {
    return { crumbs: [root, ...crumbs], hidden: [] };
  }
  const head = crumbs[0];
  const tail = crumbs.slice(-2);
  const hidden = crumbs.slice(1, -2);
  return { crumbs: head === undefined ? [root, ...tail] : [root, head, ...tail], hidden };
}

/** The parent directory of an absolute path; "/" is its own parent. */
export function parentPath(path: string): string {
  const cut = path.lastIndexOf("/");
  if (cut <= 0) {
    return "/";
  }
  return path.slice(0, cut);
}

// ---------------------------------------------------------------- descriptions

const KIND_WORDS: Record<string, string> = {
  dir: "directory",
  file: "file",
  symlink: "symbolic link",
  hardlink: "hard link",
  device: "device node",
  fifo: "named pipe",
};

const STATUS_WORDS: Record<RowKind, string> = {
  added: "added",
  removed: "removed",
  modified: "modified",
  contains: "contains changes",
  unchanged: "unchanged",
};

/** The entry kind as a word, from whichever side still has the entry. */
export function kindWord(row: TreeRow): string {
  const kind = row.right?.kind ?? row.left?.kind ?? "file";
  return KIND_WORDS[kind] ?? kind;
}

/**
 * The row's screen-reader sentence (DESIGN §7).
 *
 * Status is always words, never the glyph, and every number the row shows
 * visually is repeated here in spoken units — including the two columns that
 * the responsive rules drop below 1280px, which is where their data goes.
 * `aria-level`/`setsize`/`posinset` carry the position, so it is not repeated.
 */
export function describeRow(
  row: TreeRow,
  format: {
    bytes: (value: number) => string;
    count: (value: number) => string;
  },
): string {
  const kind = rowKindOf(row);
  const parts: string[] = [row.name, kindWord(row), STATUS_WORDS[kind]];
  if (isTypeChange(row)) {
    parts.push(
      `type changed from ${KIND_WORDS[row.left?.kind ?? ""] ?? "entry"} to ${
        KIND_WORDS[row.right?.kind ?? ""] ?? "entry"
      }`,
    );
  }
  const c = changeOf(row.agg);
  if (c.addedFiles > 0) {
    parts.push(`${format.count(c.addedFiles)} files added`);
  }
  if (c.removedFiles > 0) {
    parts.push(`${format.count(c.removedFiles)} files removed`);
  }
  if (c.modifiedFiles > 0) {
    parts.push(`${format.count(c.modifiedFiles)} files modified`);
  }
  const delta = byteDelta(row.agg);
  if (delta !== 0) {
    parts.push(`${delta > 0 ? "up" : "down"} ${format.bytes(Math.abs(delta))}`);
  }
  // Removed entries have no B side, so their "total" is the A-side value the
  // row shows struck through — quoting the B-side zero would be misleading.
  const gone = row.status === "removed";
  parts.push(`total ${format.bytes(gone ? row.agg.leftBytes : row.agg.rightBytes)}`);
  if (isDirRow(row)) {
    parts.push(`${format.count(gone ? row.agg.leftFiles : row.agg.rightFiles)} files`);
  }
  return parts.join(", ");
}

/** The A→B totals both absolute cells put in their `title` (RESEARCH Q11). */
export function sideTotalsTitle(
  row: TreeRow,
  format: { bytes: (value: number) => string; count: (value: number) => string },
): string {
  return (
    `A: ${format.bytes(row.agg.leftBytes)} (${format.count(row.agg.leftFiles)} files)` +
    ` → B: ${format.bytes(row.agg.rightBytes)} (${format.count(row.agg.rightFiles)} files)`
  );
}
