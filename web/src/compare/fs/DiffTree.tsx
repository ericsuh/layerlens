import {
  hotkeysCoreFeature,
  selectionFeature,
  syncDataLoaderFeature,
} from "@headless-tree/core";
import type { ItemInstance } from "@headless-tree/core";
import { useTree } from "@headless-tree/react";
import { useQueryClient } from "@tanstack/react-query";
import type { InfiniteData } from "@tanstack/react-query";
import { useVirtualizer } from "@tanstack/react-virtual";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import type { ApiError } from "../../api/client";
import { queryKeys, useTreeDirectoryQuery } from "../../api/queries";
import type { TreePage, TreeRow } from "../../api/types";
import { formatCount } from "../../lib/format";
import { serverFilter } from "../../lib/urlstate";
import type { TreeFilter } from "../../lib/urlstate";
import { TreeHeader } from "./TreeHeader";
import { TreeRowView } from "./TreeRowView";
import {
  ROW_HEIGHT,
  autoExpandedForName,
  interleaveTrailers,
  isExpandable,
  mergePages,
  parentPath,
  seedPageFromRow,
  visibleChildren,
} from "./treeAdapter";
import type { DirectoryData, LoadedRows, TrailerVariant, VisibleEntry } from "./treeAdapter";

/** How many rows ahead of the viewport a directory starts loading its next page. */
const WATERMARK_ROWS = 12;

/** Rows rendered beyond the viewport, both directions. */
const OVERSCAN = 8;

/** The skeleton trailer is three rows tall (DESIGN §5.3). */
const SKELETON_HEIGHT = ROW_HEIGHT * 3;

/** Everything that identifies one comparison — the query key minus the path. */
export interface TreeRequest {
  left: string;
  right: string;
  leftLayers: number;
  rightLayers: number;
  filter: TreeFilter;
}

export interface DirectorySnapshot {
  data: DirectoryData;
  isPending: boolean;
  isPlaceholder: boolean;
  error: ApiError | null;
  hasNextPage: boolean;
  isFetchingNextPage: boolean;
  fetchNextPage: () => void;
  retry: () => void;
}

type SnapshotMap = ReadonlyMap<string, DirectorySnapshot>;

/**
 * One directory's `useInfiniteQuery`, mounted per expanded path (§8.4).
 *
 * It renders nothing: React Query's per-key caching, retries and
 * `keepPreviousData` are all hook-shaped, and hooks cannot be called in a loop
 * over a set that changes — so each open directory gets its own component
 * instance, keyed by path, that publishes its state upward.
 */
function DirectoryLoader({
  request,
  path,
  onSnapshot,
}: {
  request: TreeRequest;
  path: string;
  onSnapshot: (path: string, snapshot: DirectorySnapshot | null) => void;
}) {
  const queryClient = useQueryClient();
  const wire = serverFilter(request.filter);
  const query = useTreeDirectoryQuery({
    left: request.left,
    right: request.right,
    leftLayers: request.leftLayers,
    rightLayers: request.rightLayers,
    path,
    filter: wire,
    enabled: true,
  });

  const pages = query.data?.pages;
  const data = useMemo(() => mergePages(pages ?? []), [pages]);
  // `placeholderData: keepPreviousData` means `query.data` can be the PREVIOUS
  // key's answer while this one loads. That is exactly right for rendering
  // (state #24 dims rather than blanks) and exactly wrong for anything that
  // writes to the cache — seeding those rows would file one filter's children
  // under another filter's key, where `staleTime: Infinity` would keep them
  // forever.
  const isPlaceholder = query.isPlaceholderData;

  // The depth=2 prefetch pays off here: a row that arrived with all of its
  // children embedded seeds that directory's own query, so expanding it is a
  // render rather than a round trip. `childrenTruncated` rows are skipped —
  // seeding a prefix would leave the directory permanently short with no
  // cursor to continue from (§6.5).
  useEffect(() => {
    if (pages === undefined || isPlaceholder) {
      return;
    }
    for (const page of pages) {
      for (const row of page.rows) {
        const seed = seedPageFromRow(row);
        if (seed === null) {
          continue;
        }
        const key = queryKeys.tree(
          request.left,
          request.right,
          request.leftLayers,
          request.rightLayers,
          row.path,
          wire,
        );
        queryClient.setQueryData<InfiniteData<TreePage, string | undefined>>(key, (old) =>
          old ?? { pages: [seed], pageParams: [undefined] },
        );
      }
    }
  }, [isPlaceholder, pages, queryClient, request, wire]);

  // A cursor the server refuses (§6.5: eviction, reassembly) is not retryable
  // by repetition. Results are deterministic, so starting the directory over
  // is loss-free — and resetting rather than surfacing an error keeps a
  // background eviction invisible to the user.
  const resetFor = useRef<string | null>(null);
  useEffect(() => {
    const error = query.error;
    const loadedPages = pages?.length ?? 0;
    if (error === null || error.code !== "bad_request" || loadedPages === 0) {
      return;
    }
    const token = `${path}:${String(loadedPages)}`;
    if (resetFor.current === token) {
      return;
    }
    resetFor.current = token;
    void queryClient.resetQueries({
      queryKey: queryKeys.tree(
        request.left,
        request.right,
        request.leftLayers,
        request.rightLayers,
        path,
        wire,
      ),
    });
  }, [query.error, pages, path, queryClient, request, wire]);

  const { fetchNextPage, refetch } = query;
  const doFetchNext = useCallback(() => {
    void fetchNextPage();
  }, [fetchNextPage]);
  const doRetry = useCallback(() => {
    void refetch();
  }, [refetch]);

  const snapshot = useMemo<DirectorySnapshot>(
    () => ({
      data,
      isPending: query.isPending,
      isPlaceholder,
      error: query.isError ? query.error : null,
      hasNextPage: query.hasNextPage,
      isFetchingNextPage: query.isFetchingNextPage,
      fetchNextPage: doFetchNext,
      retry: doRetry,
    }),
    [
      data,
      doFetchNext,
      doRetry,
      query.error,
      query.hasNextPage,
      query.isError,
      query.isFetchingNextPage,
      query.isPending,
      isPlaceholder,
    ],
  );

  useEffect(() => {
    onSnapshot(path, snapshot);
  }, [onSnapshot, path, snapshot]);

  useEffect(
    () => () => {
      onSnapshot(path, null);
    },
    [onSnapshot, path],
  );

  return null;
}

const EMPTY_ROWS: readonly TreeRow[] = [];

export interface DiffTreeProps {
  request: TreeRequest;
  rootPath: string;
  nameFilter: string;
  /** Drill-down: re-roots the view by writing the URL's `path`. */
  onNavigate: (path: string) => void;
  /** "Show all entries" from the empty states. */
  onShowAll: () => void;
  /** Root aggregate + status, lifted so the panel header can show them. */
  onRootData?: (data: DirectoryData | null) => void;
  /** The file row the user selected, for the panel's detail line. */
  onSelectRow?: (row: TreeRow | null) => void;
}

/**
 * The virtualized unified diff tree.
 *
 * headless-tree owns tree semantics (expansion, focus, keyboard, ARIA) over a
 * synchronous data loader backed by the per-directory queries; TanStack
 * Virtual renders only the rows in view; `treeAdapter` supplies every pure
 * decision in between. Synthetic rows — skeletons, "Show N more…", per
 * directory errors — are spliced into the flat list at the point each
 * directory closes, so they are virtualized like everything else.
 */
export function DiffTree({
  request,
  rootPath,
  nameFilter,
  onNavigate,
  onShowAll,
  onRootData,
  onSelectRow,
}: DiffTreeProps) {
  const queryClient = useQueryClient();
  const wire = serverFilter(request.filter);
  const [snapshots, setSnapshots] = useState<SnapshotMap>(() => new Map());
  const [userExpanded, setUserExpanded] = useState<string[]>([]);
  const [focusedItem, setFocusedItem] = useState<string | null>(null);
  const [selectedItems, setSelectedItems] = useState<string[]>([]);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  const handleSnapshot = useCallback((path: string, snapshot: DirectorySnapshot | null) => {
    setSnapshots((previous) => {
      if (snapshot === null) {
        if (!previous.has(path)) {
          return previous;
        }
        const next = new Map(previous);
        next.delete(path);
        return next;
      }
      if (previous.get(path) === snapshot) {
        return previous;
      }
      const next = new Map(previous);
      next.set(path, snapshot);
      return next;
    });
  }, []);

  // The pair identifies the paths themselves; a different pair is a different
  // filesystem, so the disclosure set cannot survive it. A *selection* change
  // keeps it: the paths still mean the same thing, and collapsing the user's
  // work every time they nudge a layer point is what state #24's
  // dim-don't-blank rule exists to avoid (DECISIONS, phase 007 delta).
  const pairKey = `${request.left}|${request.right}`;
  const lastPair = useRef(pairKey);
  useEffect(() => {
    if (lastPair.current === pairKey) {
      return;
    }
    lastPair.current = pairKey;
    setUserExpanded([]);
    setSelectedItems([]);
    setFocusedItem(null);
  }, [pairKey]);

  const needle = nameFilter.trim().toLowerCase();

  // Rows come from the mounted directories first and from the query cache
  // second. The cache matters because of the depth=2 prefetch: a directory's
  // children can be *known* without that directory being expanded, and the
  // name filter should find `util.js` under a collapsed `src/` rather than
  // pretend it does not exist. Memoized per render generation because the
  // flatten walk calls this once per row.
  const loaded = useMemo<LoadedRows>(() => {
    const memo = new Map<string, readonly TreeRow[] | undefined>();
    return (path: string) => {
      if (memo.has(path)) {
        return memo.get(path);
      }
      const snapshot = snapshots.get(path);
      let rows: readonly TreeRow[] | undefined = snapshot?.data.rows;
      if (rows === undefined) {
        const cached = queryClient.getQueryData<InfiniteData<TreePage, string | undefined>>(
          queryKeys.tree(
            request.left,
            request.right,
            request.leftLayers,
            request.rightLayers,
            path,
            wire,
          ),
        );
        rows = cached === undefined ? undefined : mergePages(cached.pages).rows;
      }
      memo.set(path, rows);
      return rows;
    };
  }, [queryClient, request, snapshots, wire]);
  const visibility = useMemo(
    () => ({ filter: request.filter, needle, loaded }),
    [request.filter, needle, loaded],
  );

  const autoExpanded = useMemo(
    () => autoExpandedForName(rootPath, visibility),
    [rootPath, visibility],
  );

  const expandedItems = useMemo<string[]>(() => {
    const set = new Set<string>(autoExpanded);
    for (const path of userExpanded) {
      if (path === rootPath || path.startsWith(rootPath === "/" ? "/" : `${rootPath}/`)) {
        set.add(path);
      }
    }
    return [...set].sort();
  }, [autoExpanded, rootPath, userExpanded]);

  // A directory is only worth mounting a query for when it is reachable: the
  // root always, plus every expanded directory all of whose ancestors are
  // expanded too. Anything deeper is expanded state the user cannot currently
  // see, and paying for it would turn a collapse-and-reopen into a refetch
  // storm.
  const mountedPaths = useMemo(() => {
    const open = new Set(expandedItems);
    const paths = [rootPath];
    for (const path of expandedItems) {
      let ancestor = parentPath(path);
      let reachable = true;
      while (ancestor !== rootPath && ancestor !== "/") {
        if (!open.has(ancestor)) {
          reachable = false;
          break;
        }
        ancestor = parentPath(ancestor);
      }
      if (reachable && ancestor === rootPath) {
        paths.push(path);
      }
    }
    return paths;
  }, [expandedItems, rootPath]);

  const rootSnapshot = snapshots.get(rootPath);
  const rootData = rootSnapshot?.data;
  useEffect(() => {
    onRootData?.(rootData ?? null);
  }, [onRootData, rootData]);

  // ---------------------------------------------------------------- tree

  const rowIndex = useMemo(() => {
    const index = new Map<string, TreeRow>();
    for (const [, snapshot] of snapshots) {
      for (const row of snapshot.data.rows) {
        index.set(row.path, row);
      }
    }
    return index;
  }, [snapshots]);

  const placeholderRow = useCallback(
    (path: string): TreeRow => ({
      name: path.slice(path.lastIndexOf("/") + 1),
      path,
      status: "unchanged",
      agg: { leftBytes: 0, rightBytes: 0, leftFiles: 0, rightFiles: 0 },
      hasChildren: false,
      childCount: 0,
    }),
    [],
  );

  const rootRow = useMemo<TreeRow>(
    () => ({
      name: rootPath,
      path: rootPath,
      status: rootData?.pathStatus ?? "unchanged",
      agg: rootData?.pathAgg ?? { leftBytes: 0, rightBytes: 0, leftFiles: 0, rightFiles: 0 },
      hasChildren: true,
      childCount: rootData?.totalRows ?? 0,
    }),
    [rootData, rootPath],
  );

  const childIds = useCallback(
    (path: string): string[] => visibleChildren(path, visibility).map((row) => row.path),
    [visibility],
  );

  const tree = useTree<TreeRow>({
    rootItemId: rootPath,
    getItemName: (item) => item.getItemData()?.name ?? "",
    isItemFolder: (item) => {
      const data = item.getItemData();
      return data === undefined ? false : isExpandable(data);
    },
    dataLoader: {
      // The root is synthetic (it is the *parent* of the listing, so no page
      // ever contains it) and an id can briefly outlive its data across a
      // rebuild, so neither case may return undefined.
      getItem: (itemId) =>
        itemId === rootPath ? rootRow : (rowIndex.get(itemId) ?? placeholderRow(itemId)),
      getChildren: childIds,
    },
    state: {
      expandedItems,
      focusedItem,
      selectedItems,
    },
    setExpandedItems: (updater) => {
      setUserExpanded((previous) => (typeof updater === "function" ? updater(previous) : updater));
    },
    setFocusedItem: (updater) => {
      setFocusedItem((previous) => (typeof updater === "function" ? updater(previous) : updater));
    },
    setSelectedItems: (updater) => {
      setSelectedItems((previous) => (typeof updater === "function" ? updater(previous) : updater));
    },
    features: [syncDataLoaderFeature, selectionFeature, hotkeysCoreFeature],
    ignoreHotkeysOnInputs: true,
  });

  // headless-tree caches its flat item list and rebuilds it only when the
  // expansion state changes. Our rows arrive asynchronously, so every change
  // to the loaded data or the active filters has to say so explicitly — and
  // *only* then: rebuilding on every render would loop, because a rebuild
  // pushes tree state back into React.
  const dataVersion = useMemo(
    () => ({ rowIndex, visibility, rootRow }),
    [rowIndex, visibility, rootRow],
  );
  useEffect(() => {
    void dataVersion;
    tree.rebuildTree();
  }, [tree, dataVersion]);

  const items = tree.getItems();

  const expandedSet = useMemo(() => new Set(expandedItems), [expandedItems]);

  const trailerFor = useCallback(
    (dirPath: string): TrailerVariant | null => {
      const snapshot = snapshots.get(dirPath);
      if (snapshot === undefined || (snapshot.isPending && snapshot.data.rows.length === 0)) {
        return "loading";
      }
      if (snapshot.error !== null) {
        return "error";
      }
      if (snapshot.hasNextPage) {
        return "more";
      }
      // The root's own emptiness is a whole-panel state (#25/#26/#29), not a
      // row — only nested directories get the inline note.
      if (dirPath !== rootPath && childIds(dirPath).length === 0) {
        return "empty";
      }
      return null;
    },
    [childIds, rootPath, snapshots],
  );

  const entries = useMemo(
    () =>
      interleaveTrailers(
        items.map((item) => item.getItemMeta()),
        {
          rootPath,
          isExpandedDir: (itemId) => expandedSet.has(itemId),
          trailerFor,
        },
      ),
    [expandedSet, items, rootPath, trailerFor],
  );

  const entriesRef = useRef<VisibleEntry[]>(entries);
  entriesRef.current = entries;

  const virtualizer = useVirtualizer({
    count: entries.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: (index) => {
      const entry = entries[index];
      return entry?.kind === "trailer" && entry.variant === "loading"
        ? SKELETON_HEIGHT
        : ROW_HEIGHT;
    },
    overscan: OVERSCAN,
    getItemKey: (index) => entries[index]?.id ?? index,
  });

  const virtualItems = virtualizer.getVirtualItems();

  // Watermark paging (§8.4 step 3): a directory starts its next page while its
  // tail is still WATERMARK_ROWS below the viewport, so scrolling a wide
  // directory does not stutter at every page boundary.
  const lastVisible = virtualItems[virtualItems.length - 1]?.index ?? 0;
  useEffect(() => {
    const list = entriesRef.current;
    const limit = Math.min(list.length, lastVisible + WATERMARK_ROWS);
    for (let index = 0; index < limit; index += 1) {
      const entry = list[index];
      if (entry?.kind !== "trailer" || entry.variant !== "more" || index < lastVisible - 200) {
        continue;
      }
      const snapshot = snapshots.get(entry.dirPath);
      if (snapshot !== undefined && snapshot.hasNextPage && !snapshot.isFetchingNextPage) {
        snapshot.fetchNextPage();
      }
    }
  }, [lastVisible, snapshots, entries]);

  // `aria-setsize` must be the server's post-filter child count, not the
  // number of rows this client has paged in — otherwise a screen reader
  // announces "3 of 12" in a directory of 2 500. It is only knowable while no
  // client-side narrowing is active; under a refinement or a name filter the
  // loaded, visible count headless-tree computes is the honest answer.
  const serverSetSize = useCallback(
    (path: string): number | undefined => {
      if (needle !== "" || (request.filter !== "all" && request.filter !== "changed")) {
        return undefined;
      }
      const parent = parentPath(path);
      if (parent === rootPath || path === rootPath) {
        return snapshots.get(rootPath)?.data.totalRows;
      }
      return rowIndex.get(parent)?.childCount;
    },
    [needle, request.filter, rootPath, rowIndex, snapshots],
  );

  const toggle = useCallback((path: string) => {
    setUserExpanded((previous) =>
      previous.includes(path) ? previous.filter((item) => item !== path) : [...previous, path],
    );
  }, []);

  // First paint should show something, not one collapsed row. When the root
  // has exactly one child worth opening — which is the shape of every
  // "changed only" view of a real image, where all the change sits under one
  // directory — walk that single-child spine open. It stops at the first
  // branch, runs once per comparison, and a user collapse is never undone.
  const autoOpenedFor = useRef<string | null>(null);
  useEffect(() => {
    const key = `${pairKey}|${String(request.leftLayers)}|${String(request.rightLayers)}|${rootPath}|${request.filter}`;
    if (autoOpenedFor.current === key) {
      return;
    }
    const snapshot = snapshots.get(rootPath);
    if (snapshot === undefined || snapshot.isPending) {
      return;
    }
    autoOpenedFor.current = key;
    const opened: string[] = [];
    let cursor = rootPath;
    while (opened.length < 3) {
      const children = visibleChildren(cursor, visibility);
      const only = children.length === 1 ? children[0] : undefined;
      if (only === undefined || !isExpandable(only)) {
        break;
      }
      opened.push(only.path);
      cursor = only.path;
    }
    if (opened.length > 0) {
      setUserExpanded((previous) => [...new Set([...previous, ...opened])]);
    }
  }, [pairKey, request.filter, request.leftLayers, request.rightLayers, rootPath, snapshots, visibility]);

  const selectedPath = selectedItems[0] ?? null;
  useEffect(() => {
    onSelectRow?.(selectedPath === null ? null : (rowIndex.get(selectedPath) ?? null));
  }, [onSelectRow, rowIndex, selectedPath]);

  const isStale = rootSnapshot?.isPlaceholder ?? false;

  return (
    <>
      {mountedPaths.map((path) => (
        <DirectoryLoader
          key={path}
          path={path}
          request={request}
          onSnapshot={handleSnapshot}
        />
      ))}

      <div className="relative flex min-h-0 flex-1 flex-col">
        {isStale ? (
          <span className="ll-progress" role="progressbar" aria-label="Loading the new comparison">
            <i />
          </span>
        ) : null}
        <div
          ref={scrollRef}
          className={`min-h-0 flex-1 overflow-auto${isStale ? " ll-tree-stale" : ""}`}
          data-testid="tree-scroll"
        >
          <TreeHeader />
          {rootSnapshot === undefined || (rootSnapshot.isPending && !rootSnapshot.isPlaceholder) ? (
            <TreeSkeleton />
          ) : rootSnapshot.error !== null && rootSnapshot.data.rows.length === 0 ? (
            // Only a *first* page failure blanks the panel. A failed second
            // page keeps everything already loaded and puts the error on the
            // trailing row, where the retry belongs.
            <TreeErrorState message={rootSnapshot.error.message} onRetry={rootSnapshot.retry} />
          ) : entries.length === 0 ? (
            <TreeEmptyState
              request={request}
              rootPath={rootPath}
              nameFilter={nameFilter}
              rootData={rootSnapshot.data}
              onShowAll={onShowAll}
            />
          ) : (
            <div
              {...tree.getContainerProps("Filesystem diff")}
              className="relative w-full"
              data-testid="tree-rows"
              data-row-count={entries.length}
              style={{ height: `${String(virtualizer.getTotalSize())}px` }}
            >
              {virtualItems.map((virtualRow) => {
                const entry = entries[virtualRow.index];
                if (entry === undefined) {
                  return null;
                }
                return (
                  <div
                    key={virtualRow.key}
                    data-index={virtualRow.index}
                    // `presentation` so the positioning wrapper does not sit
                    // between role=tree and role=treeitem in the a11y tree.
                    role="presentation"
                    className="absolute top-0 left-0 w-full"
                    style={{ transform: `translateY(${String(virtualRow.start)}px)` }}
                  >
                    {entry.kind === "item" ? (
                      <ItemRow
                        item={items[entry.itemIndex]}
                        level={entry.level}
                        focusedItem={focusedItem}
                        firstItemId={items[0]?.getId() ?? null}
                        maxSiblingBytes={
                          snapshots.get(parentPath(entry.id))?.data.maxSiblingBytes ?? 0
                        }
                        ariaSetSize={serverSetSize(entry.id)}
                        expanded={expandedSet.has(entry.id)}
                        onToggle={toggle}
                        onDrill={onNavigate}
                      />
                    ) : (
                      <Trailer
                        entry={entry}
                        snapshot={snapshots.get(entry.dirPath)}
                        loadedRows={snapshots.get(entry.dirPath)?.data.rows ?? EMPTY_ROWS}
                      />
                    )}
                  </div>
                );
              })}
            </div>
          )}
        </div>
      </div>
    </>
  );
}

function ItemRow({
  item,
  level,
  focusedItem,
  firstItemId,
  maxSiblingBytes,
  ariaSetSize,
  expanded,
  onToggle,
  onDrill,
}: {
  item: ItemInstance<TreeRow> | undefined;
  level: number;
  focusedItem: string | null;
  firstItemId: string | null;
  maxSiblingBytes: number;
  ariaSetSize: number | undefined;
  expanded: boolean;
  onToggle: (path: string) => void;
  onDrill: (path: string) => void;
}) {
  if (item === undefined) {
    return null;
  }
  const row = item.getItemData();
  if (row === undefined) {
    return null;
  }
  // headless-tree's own row props toggle expansion from anywhere in the row
  // and treat "nothing focused" as "everything focusable". DESIGN §5.3 wants
  // three *distinct* affordances and a roving tabindex, so the click handler
  // is dropped and the tab stop is decided here.
  // headless-tree's own row props toggle expansion from anywhere in the row
  // and treat "nothing focused" as "everything focusable". DESIGN §5.3 wants
  // three *distinct* affordances and a roving tabindex, so its click handler
  // and tab stop are dropped and replaced below.
  const itemProps: Record<string, unknown> = { ...item.getProps() };
  delete itemProps.onClick;
  delete itemProps.tabIndex;
  const id = item.getId();
  const focused = focusedItem === null ? id === firstItemId : focusedItem === id;

  return (
    <TreeRowView
      row={row}
      level={level}
      expanded={expanded}
      selected={item.isSelected()}
      maxSiblingBytes={maxSiblingBytes}
      onToggle={() => {
        item.setFocused();
        onToggle(id);
      }}
      onDrill={() => {
        onDrill(id);
      }}
      onSelect={() => {
        item.setFocused();
        item.select();
      }}
      itemProps={{
        ...(itemProps as Record<string, never>),
        tabIndex: focused ? 0 : -1,
        ...(ariaSetSize === undefined ? {} : { "aria-setsize": ariaSetSize }),
      }}
    />
  );
}

function Trailer({
  entry,
  snapshot,
  loadedRows,
}: {
  entry: Extract<VisibleEntry, { kind: "trailer" }>;
  snapshot: DirectorySnapshot | undefined;
  loadedRows: readonly TreeRow[];
}) {
  const indent = { paddingLeft: `${String(8 + entry.level * 20 + 24)}px` };
  if (entry.variant === "loading") {
    return (
      <div style={indent} data-testid="tree-loading" aria-label="Loading entries">
        {[86, 64, 72].map((width) => (
          <div key={width} className="flex h-8 items-center px-2">
            <div className="ll-skeleton h-3" style={{ width: `${String(width)}%` }} />
          </div>
        ))}
      </div>
    );
  }
  if (entry.variant === "error") {
    return (
      <div className="ll-trailer" style={indent} role="alert" data-testid="tree-row-error">
        <span className="text-removed-strong truncate">
          {snapshot?.error?.message ?? "This directory could not be loaded."}
        </span>
        <button
          type="button"
          className="ll-btn-ghost flex-none"
          onClick={() => {
            snapshot?.retry();
          }}
        >
          Retry
        </button>
      </div>
    );
  }
  if (entry.variant === "empty") {
    return (
      <div className="ll-trailer" style={indent}>
        No entries here.
      </div>
    );
  }
  const remaining = Math.max(0, (snapshot?.data.totalRows ?? 0) - loadedRows.length);
  return (
    <div className="ll-trailer" style={indent}>
      <button
        type="button"
        className="ll-more-btn"
        data-testid="show-more"
        disabled={snapshot?.isFetchingNextPage ?? false}
        onClick={() => {
          snapshot?.fetchNextPage();
        }}
      >
        {snapshot?.isFetchingNextPage === true
          ? "Loading more…"
          : `Show ${formatCount(remaining)} more…`}
      </button>
    </div>
  );
}

function TreeSkeleton() {
  return (
    <div className="p-2" data-testid="tree-skeleton">
      {[86, 64, 72, 55, 68, 48, 60, 40].map((width, index) => (
        <div key={width * 100 + index} className="flex h-8 items-center gap-3 px-2">
          <div className="ll-skeleton h-3" style={{ width: `${String(width)}%` }} />
          <div className="ll-skeleton ml-auto h-3 w-16 flex-none" />
        </div>
      ))}
    </div>
  );
}

function TreeErrorState({ message, onRetry }: { message: string; onRetry: () => void }) {
  return (
    <div className="px-6 py-14 text-center" role="alert" data-testid="tree-error">
      <p className="text-section text-removed-strong m-0 mb-1.5">This directory could not be loaded</p>
      <p className="text-text-muted mx-auto m-0 max-w-[420px] [overflow-wrap:anywhere]">{message}</p>
      <button type="button" className="ll-btn-ghost mt-4" onClick={onRetry}>
        Retry
      </button>
    </div>
  );
}

/** States #25, #26 and #29 — the three ways a tree can legitimately be empty. */
function TreeEmptyState({
  request,
  rootPath,
  nameFilter,
  rootData,
  onShowAll,
}: {
  request: TreeRequest;
  rootPath: string;
  nameFilter: string;
  rootData: DirectoryData;
  onShowAll: () => void;
}) {
  // Only mounted in the empty state, and deliberately: it answers "how many
  // entries is the filter hiding?" — a number we cannot derive from a
  // `changed` response — and warms the exact cache entry the "Show all"
  // button switches to.
  const unfiltered = useTreeDirectoryQuery({
    left: request.left,
    right: request.right,
    leftLayers: request.leftLayers,
    rightLayers: request.rightLayers,
    path: rootPath,
    filter: "all",
    enabled: request.filter !== "all",
  });
  const hiddenCount = unfiltered.data?.pages[0]?.totalRows;

  if (nameFilter.trim() !== "") {
    return (
      <EmptyBlock
        title={`No entries match “${nameFilter.trim()}”`}
        detail={`Nothing under ${rootPath} that has been loaded matches that name. The name filter searches the entries already fetched — expand or drill into a directory to search deeper.`}
        testId="tree-empty-name"
      />
    );
  }

  // "The filesystems are identical" is a claim about the whole comparison, so
  // it may only be made at the real root. An unchanged directory *inside* a
  // comparison that does differ is state #26, not state #25.
  const identical =
    rootPath === "/" &&
    rootData.pathAgg.leftBytes === rootData.pathAgg.rightBytes &&
    rootData.pathAgg.leftFiles === rootData.pathAgg.rightFiles &&
    rootData.pathStatus === "unchanged";

  if (identical) {
    return (
      <EmptyBlock
        title="No differences — the filesystems are identical at these layers"
        detail="Both comparison points resolve to the same cumulative filesystem. Select a layer after the fork in each image to see what changed."
        testId="tree-empty-identical"
        {...(request.filter === "all"
          ? {}
          : { action: { label: "Show all entries", onClick: onShowAll } })}
      />
    );
  }

  if (request.filter !== "all") {
    return (
      <EmptyBlock
        title="No changes in this directory"
        detail={
          hiddenCount === undefined
            ? `Every entry under ${rootPath} is unchanged and hidden by the current filter.`
            : `${formatCount(hiddenCount)} unchanged ${
                hiddenCount === 1 ? "entry is" : "entries are"
              } hidden by the current filter.`
        }
        testId="tree-empty-filtered"
        action={{ label: "Show all entries", onClick: onShowAll }}
      />
    );
  }

  return (
    <EmptyBlock
      title="Empty directory"
      detail={`${rootPath} contains no entries in either image.`}
      testId="tree-empty-dir"
    />
  );
}

function EmptyBlock({
  title,
  detail,
  testId,
  action,
}: {
  title: string;
  detail: string;
  testId: string;
  action?: { label: string; onClick: () => void };
}) {
  return (
    <div className="px-6 py-14 text-center" data-testid={testId}>
      <p className="text-section m-0 mb-1.5">{title}</p>
      <p className="text-text-muted mx-auto m-0 max-w-[460px]">{detail}</p>
      {action ? (
        <button type="button" className="ll-btn-ghost mt-4" onClick={action.onClick}>
          {action.label}
        </button>
      ) : null}
    </div>
  );
}
