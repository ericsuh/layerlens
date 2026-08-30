import type { Box } from "./geometry";
import { branchSpinePaths, forkPath, trunkSpinePath } from "./geometry";

export interface OverlayEdge {
  key: string;
  d: string;
  highlighted: boolean;
}

export interface EdgeOverlayProps {
  width: number;
  height: number;
  trunk: Box[];
  left: Box[];
  right: Box[];
  couldBeShared: OverlayEdge[];
  /**
   * False in the strict-prefix case: the trunk runs straight on into the
   * extending image's cards, so there is nothing to fork. The extension's
   * boxes are then passed as that side's `left`/`right` with the last trunk
   * card prepended, which makes the branch spine draw the join in that
   * image's accent.
   */
  showFork?: boolean;
}

/**
 * The SVG behind the layer cards: trunk spine, the fork elbow into each
 * branch, the branch spines, and the dotted could-be-shared arcs.
 *
 * It is `pointer-events: none` and `aria-hidden`: everything it draws is
 * either decoration or duplicated by a real control (the `≈` chip on each card
 * and the midpoint pill, both focusable buttons), so the diagram is fully
 * usable with the SVG ignored.
 */
export function EdgeOverlay({
  width,
  height,
  trunk,
  left,
  right,
  couldBeShared,
  showFork = true,
}: EdgeOverlayProps) {
  const spine = trunkSpinePath(trunk);
  const trunkLast = trunk[trunk.length - 1];
  const leftFirst = left[0];
  const rightFirst = right[0];

  return (
    <svg
      className="ll-edges"
      viewBox={`0 0 ${String(Math.max(width, 1))} ${String(Math.max(height, 1))}`}
      width={width}
      height={height}
      aria-hidden="true"
      focusable="false"
      data-testid="edge-overlay"
    >
      {spine === null ? null : <path className="ll-edge-trunk" d={spine} />}

      {showFork && trunkLast !== undefined && leftFirst !== undefined ? (
        <path className="ll-edge-a" d={forkPath(trunkLast, leftFirst)} data-testid="fork-a" />
      ) : null}
      {showFork && trunkLast !== undefined && rightFirst !== undefined ? (
        <path className="ll-edge-b" d={forkPath(trunkLast, rightFirst)} data-testid="fork-b" />
      ) : null}

      {/*
        Keyed by position, not by the path data. Spine segments are purely
        positional geometry — one per gap between consecutive branch cards, in a
        list that never reorders — while the `d` strings collide whenever two
        segments are degenerate, which is every segment before the cards have
        been measured (and permanently under jsdom, where every rect is zero).
        That collision made React warn about duplicate keys and is licence for
        it to drop or duplicate a node.
      */}
      {branchSpinePaths(left).map((d, i) => (
        <path className="ll-edge-spine-a" d={d} key={`spine-a-${i}`} />
      ))}
      {branchSpinePaths(right).map((d, i) => (
        <path className="ll-edge-spine-b" d={d} key={`spine-b-${i}`} />
      ))}

      {couldBeShared.map((edge) => (
        <path
          key={edge.key}
          className={`ll-edge-dotted ${edge.highlighted ? "ll-edge-highlighted" : ""}`.trim()}
          d={edge.d}
          data-testid="could-be-shared-edge"
        />
      ))}
    </svg>
  );
}
