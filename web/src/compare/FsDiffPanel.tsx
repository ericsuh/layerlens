import { useCallback, useMemo, useState } from "react";

import type { TreeRow } from "../api/types";
import { formatBytes, formatCount, formatMode } from "../lib/format";
import type { CompareUrlState, TreeFilter } from "../lib/urlstate";
import { Breadcrumbs } from "./fs/Breadcrumbs";
import { DiffTree } from "./fs/DiffTree";
import type { TreeRequest } from "./fs/DiffTree";
import { FilterToggle } from "./fs/FilterToggle";
import { Legend } from "./fs/Legend";
import { TreeHeader } from "./fs/TreeHeader";
import { kindWord } from "./fs/treeAdapter";
import type { DirectoryData } from "./fs/treeAdapter";
import { selectionLabel } from "./selection";
import type { LayerSelection } from "./selection";

const SKELETON_ROWS = [86, 64, 72, 55, 68, 48, 60, 40];

/**
 * The filesystem-diff section (DESIGN §5.3).
 *
 * It owns the shareable tree state — the drill-down root and the filter both
 * live in the URL — and nothing else: the tree's disclosure set, focus and
 * loading are `DiffTree`'s, and the name filter is deliberately *not* in the
 * URL because it searches only the window the client has loaded, so a pasted
 * link could not reproduce its result.
 */
export function FsDiffPanel({
  urlState,
  selection,
  onUrlChange,
}: {
  urlState: CompareUrlState;
  selection: LayerSelection | null;
  onUrlChange: (next: Partial<CompareUrlState>) => void;
}) {
  const [nameFilter, setNameFilter] = useState("");
  const [rootData, setRootData] = useState<DirectoryData | null>(null);
  const [selectedRow, setSelectedRow] = useState<TreeRow | null>(null);

  const { left, right, path, filter } = urlState;

  const request = useMemo<TreeRequest | null>(
    () =>
      left === null || right === null || selection === null
        ? null
        : {
            left,
            right,
            leftLayers: selection.l,
            rightLayers: selection.r,
            filter,
          },
    [filter, left, right, selection],
  );

  const navigate = useCallback(
    (next: string) => {
      onUrlChange({ path: next });
    },
    [onUrlChange],
  );
  const showAll = useCallback(() => {
    onUrlChange({ filter: "all" });
  }, [onUrlChange]);
  const changeFilter = useCallback(
    (next: TreeFilter) => {
      onUrlChange({ filter: next });
    },
    [onUrlChange],
  );

  return (
    <section className="flex min-h-0 flex-col" aria-labelledby="fs-section-title">
      <header className="mb-4 flex flex-none flex-wrap items-baseline gap-2.5">
        <h2 id="fs-section-title" className="text-section m-0">
          Filesystem diff
        </h2>
        <span className="text-text-muted text-[12px]">
          cumulative up to the selected layers · B relative to A
        </span>
      </header>

      <div className="border-border bg-surface shadow-panel flex min-h-0 flex-1 flex-col rounded-[10px] border">
        <div className="border-border flex flex-none flex-col gap-2 border-b px-3.5 py-2.5">
          <Breadcrumbs path={path} onNavigate={navigate} />
          <div className="flex flex-wrap items-center gap-2.5">
            <span className="text-text-muted text-[12px]">
              {selection === null ? (
                <span className="ll-skeleton inline-block h-3 w-48 align-middle" />
              ) : (
                <>
                  Comparing{" "}
                  <b className="text-image-a" data-testid="fs-compare-a">
                    {selectionLabel("a", selection.l)}
                  </b>{" "}
                  vs{" "}
                  <b className="text-image-b" data-testid="fs-compare-b">
                    {selectionLabel("b", selection.r)}
                  </b>
                </>
              )}
            </span>
            <div className="ml-auto flex-none">
              <Legend />
            </div>
          </div>
          <FilterToggle
            filter={filter}
            onFilterChange={changeFilter}
            nameFilter={nameFilter}
            onNameFilterChange={setNameFilter}
            shown={rootData?.rows.length ?? 0}
            total={rootData?.totalRows ?? 0}
          />
        </div>

        {request === null ? (
          <PanelSkeleton />
        ) : (
          <DiffTree
            request={request}
            rootPath={path}
            nameFilter={nameFilter}
            onNavigate={navigate}
            onShowAll={showAll}
            onRootData={setRootData}
            onSelectRow={setSelectedRow}
          />
        )}

        {filter === "changed" && (rootData?.rows.length ?? 0) > 0 ? (
          <div className="border-border text-text-muted flex flex-none items-center gap-2 border-t px-3.5 py-1.5 text-[11.5px]">
            {/* No fabricated count: the number of unchanged entries is not
                derivable from a `changed` response, and the empty state pays
                for a real one only when it has something to explain. */}
            Unchanged entries are hidden by the current filter.
            <button
              type="button"
              className="ll-link"
              data-testid="show-all-hidden"
              onClick={showAll}
            >
              Show all
            </button>
          </div>
        ) : null}

        {selectedRow === null ? null : (
          <RowDetail row={selectedRow} onClose={() => { setSelectedRow(null); }} />
        )}
      </div>
    </section>
  );
}

/** DESIGN state #18: chrome is real, only the row area is a skeleton. */
function PanelSkeleton() {
  return (
    <div aria-busy="true" className="flex min-h-0 flex-1 flex-col">
      <TreeHeader />
      <div className="flex-1 overflow-hidden p-2" data-testid="fs-skeleton">
        {SKELETON_ROWS.map((width, index) => (
          <div key={width * 100 + index} className="flex h-8 items-center gap-3 px-2">
            <div className="ll-skeleton h-3" style={{ width: `${String(width)}%` }} />
            <div className="ll-skeleton ml-auto h-3 w-16 flex-none" />
          </div>
        ))}
      </div>
      <p className="sr-only">Building the filesystem diff for the selected layers.</p>
    </div>
  );
}

function side(meta: TreeRow["left"]): string {
  if (meta === undefined) {
    return "absent";
  }
  // `implicit` says the metadata is ours, not the image's: the directory was
  // never named by a layer header and its mode is a value squashing invented,
  // so quoting it as a permission string would be a fabrication (§6.5).
  const mode = meta.implicit === true ? "—" : formatMode(meta.mode);
  const target = meta.linkTarget === undefined ? "" : ` → ${meta.linkTarget}`;
  return `${meta.kind} ${mode} ${formatBytes(meta.sizeBytes)}${target}`;
}

/** The detail line a file row shows when selected (DESIGN §5.3 "Tree rows"). */
function RowDetail({ row, onClose }: { row: TreeRow; onClose: () => void }) {
  return (
    <div
      className="border-border text-text-muted flex flex-none items-center gap-3 border-t px-3.5 py-2 text-[12px]"
      data-testid="row-detail"
    >
      <span className="ll-mono text-text min-w-0 truncate" title={row.path}>
        {row.path}
      </span>
      <span className="flex-none">
        {kindWord(row)} · A: {side(row.left)} · B: {side(row.right)} ·{" "}
        {formatCount(row.agg.rightFiles)} files
      </span>
      <button type="button" className="ll-btn-ghost ml-auto flex-none" onClick={onClose}>
        Close
      </button>
    </div>
  );
}
