import type { HTMLAttributes, MouseEvent } from "react";

import type { TreeRow } from "../../api/types";
import {
  formatByteDelta,
  formatBytes,
  formatBytesSpoken,
  formatCompactCount,
  formatCount,
} from "../../lib/format";
import { SizeBar } from "./SizeBar";
import {
  byteDelta,
  changedDescendants,
  describeRow,
  fileDelta,
  isDirRow,
  isExpandable,
  isTypeChange,
  rowKindOf,
  sideTotalsTitle,
} from "./treeAdapter";
import type { RowKind } from "./treeAdapter";

const ROW_CLASS: Record<RowKind, string> = {
  added: "ll-trow-added",
  removed: "ll-trow-removed",
  modified: "ll-trow-modified",
  // A directory that merely *contains* changes gets no tint — that is the
  // rule that separates "changed container" from "changed thing" (DESIGN §5.3).
  contains: "ll-trow-contains",
  unchanged: "ll-trow-unchanged",
};

const GLYPH: Record<Exclude<RowKind, "contains">, string> = {
  added: "+",
  removed: "−",
  modified: "±",
  unchanged: "·",
};

/** Depth past which the indent guides fade (DESIGN §5.3). */
const GUIDE_FADE_DEPTH = 8;

function Chevron() {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" aria-hidden="true">
      <path
        d="M3 1l4 4-4 4"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.6"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

const SPOKEN = { bytes: formatBytesSpoken, count: formatCount };
const WRITTEN = { bytes: formatBytes, count: formatCount };

export interface TreeRowViewProps {
  row: TreeRow;
  /** 0 at the current root — the number of indent guides to draw. */
  level: number;
  expanded: boolean;
  selected: boolean;
  maxSiblingBytes: number;
  onToggle: () => void;
  onDrill: () => void;
  onSelect: () => void;
  /** ARIA + focus props from headless-tree, minus its whole-row click handler. */
  itemProps: HTMLAttributes<HTMLDivElement> & { ref?: React.Ref<HTMLDivElement> };
}

/**
 * One tree row (DESIGN §5.3 "Tree rows").
 *
 * Three distinct affordances, deliberately not one: the chevron toggles, the
 * *name* toggles (directories) or selects (files), and the `↳` button at the
 * end of the name re-roots the view. The rest of the row is a dead zone with
 * no pointer cursor, so "clickable" never blurs into "the row happens to be
 * under my mouse".
 */
export function TreeRowView({
  row,
  level,
  expanded,
  selected,
  maxSiblingBytes,
  onToggle,
  onDrill,
  onSelect,
  itemProps,
}: TreeRowViewProps) {
  const kind = rowKindOf(row);
  const dir = isDirRow(row);
  const expandable = isExpandable(row);
  const gone = row.status === "removed";
  const changed = changedDescendants(row.agg);
  const deltaBytes = byteDelta(row.agg);
  const deltaFiles = fileDelta(row.agg);
  const totals = sideTotalsTitle(row, WRITTEN);
  const description = describeRow(row, SPOKEN);

  const deltaClass =
    kind === "added"
      ? "ll-tnum-pos"
      : kind === "removed"
        ? "ll-tnum-neg"
        : deltaBytes > 0
          ? "ll-tnum-pos"
          : deltaBytes < 0
            ? "ll-tnum-neg"
            : "ll-tnum-zero";

  const stop = (event: MouseEvent, action: () => void): void => {
    event.stopPropagation();
    action();
  };

  return (
    <div
      {...itemProps}
      className={`ll-tgrid ll-trow ${ROW_CLASS[kind]}${selected ? " ll-trow-focused" : ""}`}
      aria-label={description}
      data-testid={`tree-row-${row.path}`}
      data-status={kind}
      data-level={level}
    >
      <div className="ll-tcell-name">
        <span className="ll-tguides" aria-hidden="true">
          {Array.from({ length: level }, (_, index) => (
            <span
              key={index}
              className={`ll-tguide${index >= GUIDE_FADE_DEPTH ? " ll-tguide-faded" : ""}`}
            />
          ))}
        </span>
        <button
          type="button"
          tabIndex={-1}
          className={`ll-chev${expanded ? " ll-chev-open" : ""}${expandable ? "" : " ll-chev-leaf"}`}
          aria-hidden={expandable ? undefined : "true"}
          disabled={!expandable}
          title={expanded ? "Collapse" : "Expand"}
          onClick={(event) => {
            stop(event, onToggle);
          }}
        >
          <Chevron />
        </button>
        <button
          type="button"
          tabIndex={-1}
          className={`ll-tname${dir ? "" : " ll-tname-file"}`}
          title={`${row.path} — ${describeRow(row, WRITTEN)}`}
          onClick={(event) => {
            stop(event, expandable ? onToggle : onSelect);
          }}
        >
          {row.name}
          {dir ? "/" : ""}
        </button>
        {isTypeChange(row) ? (
          <span
            className="ll-chip flex-none"
            title={`This path is a ${row.left?.kind ?? "?"} in image A and a ${
              row.right?.kind ?? "?"
            } in image B, so it has no comparable subtree.`}
          >
            type changed
          </span>
        ) : null}
        {dir ? (
          <button
            type="button"
            tabIndex={-1}
            className="ll-drill"
            title={`Open ${row.path} as root`}
            aria-label={`Open ${row.path} as root`}
            onClick={(event) => {
              stop(event, onDrill);
            }}
          >
            ↳
          </button>
        ) : null}
      </div>

      <div className="ll-tcell-status" aria-hidden="true">
        {kind === "contains" ? (
          <span className="ll-contains" title={`${formatCount(changed)} changed descendants`}>
            ± {formatCompactCount(changed)}
          </span>
        ) : (
          GLYPH[kind]
        )}
      </div>

      <div className={`ll-tnum ${deltaClass}`} aria-hidden="true" data-testid="cell-delta-size">
        {formatByteDelta(deltaBytes)}
      </div>

      <div
        className={`ll-tnum ll-tcol-optional ${
          deltaFiles > 0 ? "ll-tnum-pos" : deltaFiles < 0 ? "ll-tnum-neg" : "ll-tnum-zero"
        }`}
        aria-hidden="true"
        data-testid="cell-delta-files"
      >
        {/* A file is not a subtree: its own add/remove is already the status
            glyph, so a "+1" here would double-count it. */}
        {dir ? (deltaFiles === 0 ? "—" : `${deltaFiles > 0 ? "+" : "−"}${formatCount(Math.abs(deltaFiles))}`) : ""}
      </div>

      <div
        className={`ll-tnum${gone ? " ll-tnum-gone" : ""}`}
        title={totals}
        aria-hidden="true"
        data-testid="cell-size"
      >
        {formatBytes(gone ? row.agg.leftBytes : row.agg.rightBytes)}
      </div>

      <div
        className={`ll-tnum ll-tcol-optional${gone ? " ll-tnum-gone" : ""}`}
        title={totals}
        aria-hidden="true"
        data-testid="cell-files"
      >
        {dir ? formatCount(gone ? row.agg.leftFiles : row.agg.rightFiles) : ""}
      </div>

      <div className="ll-tcell-bar">
        <SizeBar agg={row.agg} maxSiblingBytes={maxSiblingBytes} />
      </div>
    </div>
  );
}
