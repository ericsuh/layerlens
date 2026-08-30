import type { LayerGraph } from "../api/types";

/**
 * The layer comparison point for each side (DESIGN §5.2 "Selection model").
 *
 * Values are layer **counts**, matching the API's `leftLayers`/`rightLayers`
 * (§6.5): n means "layers 1..n are included", so a card at absolute index i
 * corresponds to count i + 1 and "everything above the selection rule is
 * included" is literally true.
 */
export interface LayerSelection {
  l: number;
  r: number;
}

/** The lengths the reducer clamps against, read off the graph. */
export interface SelectionBounds {
  trunkLength: number;
  leftLength: number;
  rightLength: number;
}

export function boundsOf(graph: LayerGraph): SelectionBounds {
  return {
    trunkLength: graph.trunkLength,
    leftLength: graph.trunk.length + graph.leftBranch.length,
    rightLength: graph.trunk.length + graph.rightBranch.length,
  };
}

/** Default on entry: the full stack on each side — the full-image diff. */
export function defaultSelection(bounds: SelectionBounds): LayerSelection {
  return { l: bounds.leftLength, r: bounds.rightLength };
}

export type SelectionAction =
  /**
   * A trunk card. Trunk layers are byte-identical on both sides, so one click
   * sets *both* points — the intentional self-diff of RESEARCH Q11, which
   * proves trunk sharing by producing an all-unchanged tree.
   */
  | { type: "select-trunk"; count: number }
  /** A branch card: moves only its own side. */
  | { type: "select-left"; count: number }
  | { type: "select-right"; count: number };

function clamp(count: number, max: number): number {
  if (!Number.isFinite(count)) {
    return max;
  }
  return Math.min(Math.max(Math.trunc(count), 0), max);
}

export function selectionReducer(
  state: LayerSelection,
  action: SelectionAction,
  bounds: SelectionBounds,
): LayerSelection {
  switch (action.type) {
    case "select-trunk": {
      // A trunk point can never exceed the trunk on either side, so one clamp
      // against trunkLength keeps l === r true by construction.
      const count = clamp(action.count, bounds.trunkLength);
      return { l: count, r: count };
    }
    case "select-left":
      return { ...state, l: clamp(action.count, bounds.leftLength) };
    case "select-right":
      return { ...state, r: clamp(action.count, bounds.rightLength) };
  }
}

/** True when both sides sit on the same shared trunk layer. */
export function isTrunkSelection(state: LayerSelection, bounds: SelectionBounds): boolean {
  return state.l === state.r && state.l > 0 && state.l <= bounds.trunkLength;
}

/** `A @ layer 6` — the label on the selection rule and the sticky chip. */
export function selectionLabel(side: "a" | "b", count: number): string {
  const letter = side === "a" ? "A" : "B";
  return count === 0 ? `${letter} @ base` : `${letter} @ layer ${count}`;
}
