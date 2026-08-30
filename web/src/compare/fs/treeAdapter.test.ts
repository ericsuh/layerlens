import { describe, expect, it } from "vitest";

import { GOLDEN_TREE_APP, GOLDEN_TREE_ROOT } from "../../fixtures";
import type { TreeAgg, TreePage, TreeRow } from "../../api/types";
import { formatBytes, formatCount } from "../../lib/format";
import {
  autoExpandedForName,
  breadcrumbTrail,
  byteDelta,
  changeOf,
  changedDescendants,
  describeRow,
  interleaveTrailers,
  isDirRow,
  isExpandable,
  isTypeChange,
  matchesRefinement,
  mergePages,
  parentPath,
  rowKindOf,
  seedPageFromRow,
  sizeBarModel,
  subtreeMatchesName,
  visibleChildren,
} from "./treeAdapter";

function agg(overrides: Partial<TreeAgg> = {}): TreeAgg {
  return { leftBytes: 0, rightBytes: 0, leftFiles: 0, rightFiles: 0, ...overrides };
}

/** `exactOptionalPropertyTypes` forbids passing an explicit `undefined`, but
 *  "this side does not exist" is exactly what an added/removed row looks like
 *  on the wire, so the builders take it deliberately. */
type RowOverrides = Partial<Omit<TreeRow, "left" | "right">> & {
  left?: TreeRow["left"] | undefined;
  right?: TreeRow["right"] | undefined;
};

/** Merges overrides and drops the keys explicitly set to `undefined`, which is
 *  how "this side is absent" is spelled on the wire. */
function build(base: TreeRow, overrides: RowOverrides): TreeRow {
  const row: Record<string, unknown> = { ...base, ...overrides };
  for (const key of Object.keys(row)) {
    if (row[key] === undefined) {
      delete row[key];
    }
  }
  return row as unknown as TreeRow;
}

function file(name: string, overrides: RowOverrides = {}): TreeRow {
  const base: TreeRow = {
    name,
    path: `/${name}`,
    status: "unchanged",
    left: { kind: "file", mode: 0o644, sizeBytes: 10 },
    right: { kind: "file", mode: 0o644, sizeBytes: 10 },
    agg: agg({ leftBytes: 10, rightBytes: 10, leftFiles: 1, rightFiles: 1 }),
    hasChildren: false,
    childCount: 0,
  };
  return build(base, overrides);
}

function dir(name: string, overrides: RowOverrides = {}): TreeRow {
  const base: TreeRow = {
    name,
    path: `/${name}`,
    status: "unchanged",
    left: { kind: "dir", mode: 0o755, sizeBytes: 0 },
    right: { kind: "dir", mode: 0o755, sizeBytes: 0 },
    agg: agg(),
    hasChildren: true,
    childCount: 1,
  };
  return build(base, overrides);
}

function page(rows: TreeRow[], overrides: Partial<TreePage> = {}): TreePage {
  return {
    path: "/",
    rows,
    totalRows: rows.length,
    maxSiblingBytes: 100,
    pathStatus: "modified",
    pathAgg: agg(),
    ...overrides,
  };
}

describe("changeOf", () => {
  it("reads an omitted breakdown as zero", () => {
    // The server omits all seven when zero (§6.5); an `undefined` leaking into
    // arithmetic would render every unchanged row as NaN.
    expect(changeOf(agg())).toEqual({
      addedBytes: 0,
      removedBytes: 0,
      modifiedBytesLeft: 0,
      modifiedBytesRight: 0,
      addedFiles: 0,
      removedFiles: 0,
      modifiedFiles: 0,
    });
    expect(changedDescendants(agg())).toBe(0);
  });

  it("sums the three file breakdowns for the directory summary", () => {
    expect(changedDescendants(agg({ addedFiles: 61, removedFiles: 3, modifiedFiles: 2 }))).toBe(66);
  });

  it("matches the real wire payload", () => {
    const app = GOLDEN_TREE_ROOT.rows[0];
    expect(app?.name).toBe("app");
    expect(changedDescendants(app?.agg ?? agg())).toBe(66);
    expect(byteDelta(app?.agg ?? agg())).toBe(
      (app?.agg.rightBytes ?? 0) - (app?.agg.leftBytes ?? 0),
    );
  });
});

describe("row classification", () => {
  it("separates a changed container from a changed thing", () => {
    // The distinction DESIGN §5.3 is built around: a directory whose subtree
    // changed is "contains", and gets no tint.
    expect(rowKindOf(dir("a", { status: "modified", agg: agg({ modifiedFiles: 2 }) }))).toBe(
      "contains",
    );
    // A directory the server calls modified with nothing changed underneath —
    // a mode change, or the dir↔file exception — is a changed thing.
    expect(rowKindOf(dir("a", { status: "modified" }))).toBe("modified");
    expect(rowKindOf(file("f", { status: "modified" }))).toBe("modified");
    expect(rowKindOf(file("f", { status: "added" }))).toBe("added");
    expect(rowKindOf(file("f"))).toBe("unchanged");
  });

  it("offers a disclosure triangle only where there is something to disclose", () => {
    expect(isExpandable(dir("a"))).toBe(true);
    // An empty directory is still a directory (trailing slash) but has nothing
    // to expand.
    const empty = dir("a", { hasChildren: false, childCount: 0 });
    expect(isDirRow(empty)).toBe(true);
    expect(isExpandable(empty)).toBe(false);
  });

  it("detects the dir↔file exception", () => {
    const swapped = file("config", {
      status: "modified",
      left: { kind: "file", mode: 0o644, sizeBytes: 128 },
      right: { kind: "dir", mode: 0o755, sizeBytes: 0 },
    });
    expect(isTypeChange(swapped)).toBe(true);
    expect(isTypeChange(file("f"))).toBe(false);
    // An added row has no left side and is not a type change.
    expect(isTypeChange(file("f", { status: "added", left: undefined }))).toBe(false);
  });
});

describe("mergePages", () => {
  it("appends pages in order and drops duplicate paths", () => {
    const first = page([dir("a"), file("b")], { totalRows: 4, maxSiblingBytes: 500 });
    // A resumed cursor can land on a row already seen; a duplicated React key
    // is a rendering bug, so the merge guards rather than hoping.
    const second = page([file("b"), file("c")], { totalRows: 4, maxSiblingBytes: 999 });
    const merged = mergePages([first, second]);
    expect(merged.rows.map((row) => row.name)).toEqual(["a", "b", "c"]);
    // Both page-stable numbers come from the first page, so later pages can
    // never silently rescale the bars.
    expect(merged.totalRows).toBe(4);
    expect(merged.maxSiblingBytes).toBe(500);
  });

  it("is empty and safe with no pages at all", () => {
    const merged = mergePages([]);
    expect(merged.rows).toEqual([]);
    expect(merged.totalRows).toBe(0);
    expect(merged.maxSiblingBytes).toBe(0);
    expect(merged.pathStatus).toBe("unchanged");
  });

  it("merges the real wire pages", () => {
    expect(mergePages([GOLDEN_TREE_ROOT]).rows.map((row) => row.name)).toEqual([
      "app",
      "etc",
      "usr",
      "var",
    ]);
    expect(mergePages([GOLDEN_TREE_APP]).totalRows).toBe(GOLDEN_TREE_APP.totalRows);
  });
});

describe("seedPageFromRow", () => {
  it("turns embedded depth=2 children into that directory's first page", () => {
    const app = GOLDEN_TREE_ROOT.rows[0];
    const seed = seedPageFromRow(app as TreeRow);
    expect(seed).not.toBeNull();
    expect(seed?.path).toBe("/app");
    expect(seed?.rows.map((row) => row.name)).toEqual(app?.children?.map((row) => row.name));
    expect(seed?.totalRows).toBe(app?.childCount);
    expect(seed?.nextCursor).toBeUndefined();
    // The denominator is computable because *all* the children are present.
    const expected = Math.max(
      ...(app?.children ?? []).map((row) => row.agg.leftBytes + row.agg.rightBytes),
    );
    expect(seed?.maxSiblingBytes).toBe(expected);
  });

  it("refuses to seed a truncated child list", () => {
    // A prefix seeded under `staleTime: Infinity` would leave the directory
    // permanently short with no cursor to continue from.
    expect(seedPageFromRow(dir("a", { children: [file("x")], childrenTruncated: true }))).toBeNull();
    // `childrenTruncated` with no `children` at all is the budget-exhausted case.
    expect(seedPageFromRow(dir("a", { childrenTruncated: true }))).toBeNull();
    expect(seedPageFromRow(dir("a"))).toBeNull();
  });
});

describe("filters", () => {
  const rows = [
    dir("added-dir", { path: "/added-dir", status: "added" }),
    dir("holder", { path: "/holder", status: "modified", agg: agg({ removedFiles: 2 }) }),
    file("plain.txt", { path: "/plain.txt" }),
  ];
  const loaded = (path: string) => (path === "/" ? rows : undefined);

  it("passes everything through for the server's own two filters", () => {
    for (const row of rows) {
      expect(matchesRefinement(row, "all")).toBe(true);
      expect(matchesRefinement(row, "changed")).toBe(true);
    }
  });

  it("keeps a directory whose subtree carries the requested polarity", () => {
    expect(matchesRefinement(rows[1] as TreeRow, "removed")).toBe(true);
    expect(matchesRefinement(rows[1] as TreeRow, "added")).toBe(false);
    expect(matchesRefinement(rows[0] as TreeRow, "added")).toBe(true);
  });

  it("matches a name through loaded descendants only", () => {
    const tree = {
      "/": [dir("outer", { path: "/outer" })],
      "/outer": [file("needle.js", { path: "/outer/needle.js" })],
    } as Record<string, TreeRow[]>;
    const get = (path: string) => tree[path];
    expect(subtreeMatchesName(tree["/"]?.[0] as TreeRow, "needle", get)).toBe(true);
    expect(subtreeMatchesName(tree["/"]?.[0] as TreeRow, "haystack", get)).toBe(false);
    // Unloaded subtrees cannot match — the name filter searches the window the
    // client has, which is why the empty state says so.
    expect(subtreeMatchesName(tree["/"]?.[0] as TreeRow, "needle", () => undefined)).toBe(false);
  });

  it("auto-expands the ancestors of a match but not the match itself", () => {
    const tree = {
      "/": [dir("outer", { path: "/outer" })],
      "/outer": [dir("inner", { path: "/outer/inner" }), file("needle.js", { path: "/outer/needle.js" })],
      "/outer/inner": [],
    } as Record<string, TreeRow[]>;
    const options = { filter: "all" as const, needle: "needle", loaded: (p: string) => tree[p] };
    expect([...autoExpandedForName("/", options)]).toEqual(["/outer"]);
    // A directory that *is* the match stays closed: the user searched for it,
    // not for its contents.
    const byDirName = { ...options, needle: "outer" };
    expect([...autoExpandedForName("/", byDirName)]).toEqual([]);
  });

  it("combines the refinement and the name filter", () => {
    expect(
      visibleChildren("/", { filter: "added", needle: "", loaded }).map((row) => row.name),
    ).toEqual(["added-dir"]);
    expect(
      visibleChildren("/", { filter: "all", needle: "plain", loaded }).map((row) => row.name),
    ).toEqual(["plain.txt"]);
    expect(visibleChildren("/missing", { filter: "all", needle: "", loaded })).toEqual([]);
  });
});

describe("interleaveTrailers", () => {
  const items = [
    { itemId: "/a", level: 0 },
    { itemId: "/a/b", level: 1 },
    { itemId: "/a/b/c", level: 2 },
    { itemId: "/a/d", level: 1 },
    { itemId: "/e", level: 0 },
  ];

  it("preserves the flat order and adds nothing when no directory needs a trailer", () => {
    const entries = interleaveTrailers(items, {
      rootPath: "/",
      isExpandedDir: (id) => id === "/a" || id === "/a/b",
      trailerFor: () => null,
    });
    expect(entries.map((entry) => entry.id)).toEqual(["/a", "/a/b", "/a/b/c", "/a/d", "/e"]);
    expect(entries.every((entry) => entry.kind === "item")).toBe(true);
  });

  it("closes each directory after its whole subtree, deepest first", () => {
    const entries = interleaveTrailers(items, {
      rootPath: "/",
      isExpandedDir: (id) => id === "/a" || id === "/a/b",
      trailerFor: (path) => (path === "/a/b" || path === "/a" || path === "/" ? "more" : null),
    });
    expect(entries.map((entry) => (entry.kind === "item" ? entry.id : `+${entry.dirPath}`))).toEqual(
      ["/a", "/a/b", "/a/b/c", "+/a/b", "/a/d", "+/a", "/e", "+/"],
    );
  });

  it("puts an expanded but childless directory's trailer directly beneath it", () => {
    const entries = interleaveTrailers([{ itemId: "/a", level: 0 }, { itemId: "/z", level: 0 }], {
      rootPath: "/",
      isExpandedDir: (id) => id === "/a",
      trailerFor: (path) => (path === "/a" ? "loading" : null),
    });
    expect(entries.map((entry) => (entry.kind === "item" ? entry.id : entry.variant))).toEqual([
      "/a",
      "loading",
      "/z",
    ]);
    expect(entries[1]).toMatchObject({ kind: "trailer", level: 1, dirPath: "/a" });
  });

  it("always closes the root last, even with no items at all", () => {
    const entries = interleaveTrailers([], {
      rootPath: "/app",
      isExpandedDir: () => false,
      trailerFor: () => "error",
    });
    expect(entries).toEqual([
      { kind: "trailer", id: "trailer:/app:error", dirPath: "/app", level: 0, variant: "error" },
    ]);
  });
});

describe("sizeBarModel", () => {
  // The reason the denominator is now the tree's top-level maximum rather than
  // each directory's own: with one scale, "child never out-draws its parent"
  // is arithmetic rather than a thing to remember. Per-sibling scaling
  // re-stretched every level to full width, so a 3 KiB file inside a 4 MiB
  // folder drew the same bar as the folder.
  it("never draws a child wider than the parent containing it", () => {
    const scale = 8_741_966; // the largest top-level entry
    const parent = { leftBytes: 4_000_000, rightBytes: 4_000_000, leftFiles: 9, rightFiles: 9 };
    const children = [
      { leftBytes: 4_000_000, rightBytes: 4_000_000, leftFiles: 9, rightFiles: 9 }, // all of it
      { leftBytes: 1_500_000, rightBytes: 1_500_000, leftFiles: 4, rightFiles: 4 },
      { leftBytes: 1, rightBytes: 2, leftFiles: 1, rightFiles: 1 }, // a rounding-error sliver
    ];
    const parentWidth = sizeBarModel(parent, scale).widthPx;
    for (const child of children) {
      expect(sizeBarModel(child, scale).widthPx).toBeLessThanOrEqual(parentWidth);
    }
  });


  it("scales against the largest sibling and splits into change segments", () => {
    const model = sizeBarModel(
      agg({ leftBytes: 40, rightBytes: 60, addedBytes: 25, removedBytes: 5, modifiedBytesLeft: 10, modifiedBytesRight: 10 }),
      200,
      96,
    );
    // (40 + 60) / 200 of a 96px track.
    expect(model.widthPx).toBe(48);
    expect(model.segments.map((segment) => segment.kind)).toEqual([
      "unchanged",
      "modified",
      "added",
      "removed",
    ]);
    const total = model.segments.reduce((sum, segment) => sum + segment.ratio, 0);
    expect(total).toBeCloseTo(1, 10);
    expect(model.segments.find((segment) => segment.kind === "added")?.ratio).toBeCloseTo(0.25, 10);
  });

  it("guards a zero denominator and a zero numerator", () => {
    expect(sizeBarModel(agg({ leftBytes: 5, rightBytes: 5 }), 0)).toEqual({
      widthPx: 0,
      segments: [],
    });
    expect(sizeBarModel(agg(), 100)).toEqual({ widthPx: 0, segments: [] });
  });

  it("clamps a small but non-zero bar up to 2px and a full one to the track", () => {
    expect(sizeBarModel(agg({ leftBytes: 1 }), 1_000_000, 96).widthPx).toBe(2);
    expect(sizeBarModel(agg({ leftBytes: 100 }), 100, 96).widthPx).toBe(96);
    // Over-reported breakdowns shorten the neutral segment; they never produce
    // a negative one.
    const skewed = sizeBarModel(agg({ rightBytes: 10, addedBytes: 50 }), 10, 96);
    expect(skewed.segments).toEqual([{ kind: "added", ratio: 1 }]);
  });
});

describe("breadcrumbTrail", () => {
  it("is just the root at the root", () => {
    expect(breadcrumbTrail("/")).toEqual({ crumbs: [{ label: "/", path: "/" }], hidden: [] });
  });

  it("keeps every segment while the path is shallow", () => {
    const trail = breadcrumbTrail("/app/src/lib");
    expect(trail.crumbs.map((crumb) => crumb.label)).toEqual(["/", "app", "src", "lib"]);
    expect(trail.crumbs.map((crumb) => crumb.path)).toEqual(["/", "/app", "/app/src", "/app/src/lib"]);
    expect(trail.hidden).toEqual([]);
  });

  it("collapses the middle, keeping where you started and where you are", () => {
    const trail = breadcrumbTrail("/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path");
    expect(trail.crumbs.map((crumb) => crumb.label)).toEqual([
      "/",
      "app",
      "lib",
      "get-runtime-path",
    ]);
    expect(trail.hidden.map((crumb) => crumb.label)).toEqual([
      "node_modules",
      "@babel",
      "plugin-transform-runtime",
    ]);
    expect(trail.hidden[0]?.path).toBe("/app/node_modules");
  });
});

describe("parentPath", () => {
  it("floors at the root", () => {
    expect(parentPath("/app/src/util.js")).toBe("/app/src");
    expect(parentPath("/app")).toBe("/");
    expect(parentPath("/")).toBe("/");
  });
});

describe("describeRow", () => {
  const format = { bytes: formatBytes, count: formatCount };

  it("spells out status in words and repeats every column's value", () => {
    const app = GOLDEN_TREE_ROOT.rows[0] as TreeRow;
    const text = describeRow(app, format);
    expect(text).toContain("app, directory, contains changes");
    expect(text).toContain("61 files added");
    expect(text).toContain("3 files removed");
    expect(text).toContain("2 files modified");
    expect(text).toContain("307 files");
    expect(text).not.toContain("±");
  });

  it("quotes the A side for a removed row, which has no B side", () => {
    const removed = dir("apt", {
      status: "removed",
      right: undefined,
      agg: agg({ leftBytes: 1024, leftFiles: 3, removedBytes: 1024, removedFiles: 3 }),
    });
    const text = describeRow(removed, format);
    expect(text).toContain("removed");
    expect(text).toContain("total 1.0 KiB");
    expect(text).toContain("3 files");
  });
});
