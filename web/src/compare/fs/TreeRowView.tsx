import type { HTMLAttributes, MouseEvent } from "react";

import type { TreeRow } from "../../api/types";
import {
  formatByteDelta,
  formatBytes,
  formatBytesSpoken,
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
  /** Denominator for every bar in the view — see SizeBar. */
  scaleBytes: number;
  onToggle: () => void;
  onDrill: () => void;
  onSelect: () => void;
  /** ARIA + focus props from headless-tree, minus its whole-row click handler. */
  itemProps: HTMLAttributes<HTMLDivElement> & { ref?: React.Ref<HTMLDivElement> };
}

/**
 * One tree row (DESIGN §5.3 "Tree rows").
 *
 * Two distinct affordances, deliberately not one: the chevron expands the
 * subtree in place, and the *name* re-roots the view onto that directory (or
 * selects, for a file). Splitting them this way makes each obvious from its
 * shape — a triangle discloses, a name navigates — and retires the `↳` button,
 * which had to be explained. The rest of the row is a dead zone with no pointer
 * cursor, so "clickable" never blurs into "the row happens to be under my
 * mouse".
 */
export function TreeRowView({
  row,
  level,
  expanded,
  selected,
  scaleBytes,
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
          title={
            dir
              ? `Open ${row.path} — ${describeRow(row, WRITTEN)}`
              : `${row.path} — ${describeRow(row, WRITTEN)}`
          }
          onClick={(event) => {
            stop(event, dir ? onDrill : onSelect);
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
      </div>

      <div
        className="ll-tcell-status"
        aria-hidden="true"
        title={
          kind === "contains" ? `${formatCount(changed)} changed descendants` : undefined
        }
      >
        {/* Glyph only. The count that used to sit here ("± 66") read as a size
            or a file count as often as a descendant tally; it survives as this
            cell's tooltip and in the row's screen-reader sentence. */}
        {kind === "contains" ? "±" : GLYPH[kind]}
      </div>

      <div className="ll-tcell-size" title={totals} data-testid="cell-size">
        <span className={`ll-tnum${gone ? " ll-tnum-gone" : ""}`} aria-hidden="true">
          {formatBytes(gone ? row.agg.leftBytes : row.agg.rightBytes)}
        </span>
        <SizeBar agg={row.agg} scaleBytes={scaleBytes} />
      </div>

      <div className={`ll-tnum ${deltaClass}`} aria-hidden="true" data-testid="cell-delta-size">
        {formatByteDelta(deltaBytes)}
      </div>

      <div
        className={`ll-tnum ll-tcol-optional${gone ? " ll-tnum-gone" : ""}`}
        title={totals}
        aria-hidden="true"
        data-testid="cell-files"
      >
        {dir ? formatCount(gone ? row.agg.leftFiles : row.agg.rightFiles) : ""}
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
    </div>
  );
}
