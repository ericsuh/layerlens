import { BAR_TRACK_PX, sizeBarModel } from "./treeAdapter";
import type { TreeAgg } from "../../api/types";

const SEGMENT_CLASS = {
  unchanged: "ll-seg-unchanged",
  modified: "ll-seg-modified",
  added: "ll-seg-added",
  removed: "ll-seg-removed",
} as const;

/**
 * The per-sibling-normalized relative-size bar (DESIGN §5.3).
 *
 * `aria-hidden`: every number it encodes is already in the row's SR sentence,
 * and a bar read aloud as a list of percentages helps nobody.
 */
export function SizeBar({
  agg,
  maxSiblingBytes,
  trackPx = BAR_TRACK_PX,
}: {
  agg: TreeAgg;
  maxSiblingBytes: number;
  trackPx?: number;
}) {
  const model = sizeBarModel(agg, maxSiblingBytes, trackPx);
  if (model.widthPx === 0) {
    return null;
  }
  return (
    <span
      className="ll-sbar"
      aria-hidden="true"
      data-testid="size-bar"
      data-width={model.widthPx}
      style={{ width: `${String(model.widthPx)}px` }}
    >
      {model.segments.map((segment) => (
        <i
          key={segment.kind}
          className={SEGMENT_CLASS[segment.kind]}
          style={{ width: `${String(segment.ratio * 100)}%` }}
        />
      ))}
    </span>
  );
}

/** The legend's hatched swatch — the same four fills at chip size. */
export function HatchSwatch({ kind }: { kind: keyof typeof SEGMENT_CLASS }) {
  return <i className={`ll-swatch ${SEGMENT_CLASS[kind]}`} aria-hidden="true" />;
}
