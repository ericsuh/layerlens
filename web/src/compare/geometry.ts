/**
 * Pure geometry for the trunk-and-fork diagram (DECISIONS C4: hand-rolled SVG
 * over CSS-positioned cards).
 *
 * Nothing here touches the DOM. The panel measures card rectangles with refs
 * and a ResizeObserver — branch columns wrap and scroll independently, so
 * hardcoded offsets would detach the edges — and hands the measurements to
 * these functions, which is what makes the maths unit-testable without a
 * layout engine.
 *
 * All coordinates are relative to the diagram's own top-left corner.
 */

export interface Box {
  x: number;
  y: number;
  w: number;
  h: number;
}

/** How far the fork elbow's control points reach past the cards. */
const FORK_BOW = 22;
/** How far a could-be-shared edge bows above the higher of its two cards. */
const EDGE_RISE = 14;
/** Horizontal reach of the could-be-shared edge's control points. */
const EDGE_BOW = 22;

function n(value: number): string {
  // Two decimals: enough that a half-pixel card offset survives, few enough
  // that path strings are stable and readable in test failures.
  return String(Math.round(value * 100) / 100);
}

function centerX(box: Box): number {
  return box.x + box.w / 2;
}

/** The straight spine joining consecutive trunk cards, or null when there are <2. */
export function trunkSpinePath(trunk: Box[]): string | null {
  const first = trunk[0];
  const last = trunk[trunk.length - 1];
  if (first === undefined || last === undefined || trunk.length < 2) {
    return null;
  }
  const cx = centerX(first);
  return `M${n(cx)} ${n(first.y + first.h)} V${n(last.y)}`;
}

/**
 * The fork elbow: a cubic from the bottom centre of the last trunk card to the
 * top centre of a branch's first card, bowing vertically so the two ends leave
 * and arrive travelling downward.
 */
export function forkPath(trunkLast: Box, branchFirst: Box): string {
  const cx = centerX(trunkLast);
  const bx = centerX(branchFirst);
  const startY = trunkLast.y + trunkLast.h;
  return (
    `M${n(cx)} ${n(startY)} ` +
    `C ${n(cx)} ${n(startY + FORK_BOW)}, ${n(bx)} ${n(branchFirst.y - FORK_BOW)}, ` +
    `${n(bx)} ${n(branchFirst.y)}`
  );
}

/** One vertical segment per consecutive pair of cards in a branch column. */
export function branchSpinePaths(cards: Box[]): string[] {
  const paths: string[] = [];
  for (let i = 0; i + 1 < cards.length; i += 1) {
    const from = cards[i];
    const to = cards[i + 1];
    if (from === undefined || to === undefined) {
      continue;
    }
    paths.push(`M${n(centerX(from))} ${n(from.y + from.h)} V${n(to.y)}`);
  }
  return paths;
}

export interface CouldBeSharedGeometry {
  /** The dotted cubic, drawn right edge of A → left edge of B. */
  d: string;
  /** Where the `≈ same content` pill sits, centred on the edge's apex. */
  pillX: number;
  pillY: number;
}

/**
 * A could-be-shared edge. It bows *above* both cards rather than between them
 * so the pill has clear background to sit on, and so an edge between distant
 * rows still reads as one arc rather than crossing the branch spines.
 */
export function couldBeSharedEdge(left: Box, right: Box): CouldBeSharedGeometry {
  const startX = left.x + left.w;
  const startY = left.y + left.h / 2;
  const endX = right.x;
  const endY = right.y + right.h / 2;
  const apexY = Math.min(left.y, right.y) - EDGE_RISE;
  return {
    d:
      `M${n(startX)} ${n(startY)} ` +
      `C ${n(startX + EDGE_BOW)} ${n(apexY)}, ${n(endX - EDGE_BOW)} ${n(apexY)}, ` +
      `${n(endX)} ${n(endY)}`,
    pillX: startX + (endX - startX) / 2,
    pillY: apexY,
  };
}

export interface SelectionRuleBox {
  top: number;
  left: number;
  width: number;
}

/**
 * The bold horizontal rule drawn under the selected card, which makes
 * "cumulative up to here" spatially literal: everything above it is included.
 */
export function selectionRuleBox(card: Box, gap = 6): SelectionRuleBox {
  return { top: card.y + card.h + gap, left: card.x, width: card.w };
}
