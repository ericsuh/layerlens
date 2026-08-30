import type { Side } from "../components/identity";

/**
 * The A/B slot model (DESIGN §4.2).
 *
 * The design rule is "the click target always knows its destination before the
 * click": a plain row click fills the *armed* slot, which is always visible on
 * screen, and the per-row `Set A` / `Set B` buttons place explicitly so a user
 * never has to have tracked the arming state.
 */
export interface SlotState {
  /** Image ids, or null for an empty slot. */
  a: string | null;
  b: string | null;
  /** The slot a plain row click fills next. */
  armed: Side;
}

export const initialSlotState: SlotState = { a: null, b: null, armed: "a" };

export type SlotAction =
  /** Clicking a slot card makes it the target of the next plain row click. */
  | { type: "arm"; side: Side }
  /** A plain click on a source row: fill the armed slot, or toggle off. */
  | { type: "pick"; id: string }
  /** `Set A` / `Set B`: place explicitly, or toggle off if already there. */
  | { type: "set"; side: Side; id: string }
  /** The slot's ✕. */
  | { type: "clear"; side: Side };

/**
 * After a slot is filled, the *other* slot becomes armed if it is still empty,
 * so the natural "click, click" fills A then B. Once both are filled the
 * just-filled slot stays armed: the next plain click then replaces what the
 * user was most recently looking at, which is the least surprising target.
 */
function assign(state: SlotState, side: Side, id: string | null): SlotState {
  const next: SlotState = { ...state, [side]: id };
  if (id === null) {
    // Emptying a slot arms it: the obvious next action is to refill it.
    next.armed = side;
    return next;
  }
  const other: Side = side === "a" ? "b" : "a";
  next.armed = next[other] === null ? other : side;
  return next;
}

export function slotReducer(state: SlotState, action: SlotAction): SlotState {
  switch (action.type) {
    case "arm":
      return { ...state, armed: action.side };

    case "pick": {
      // Toggle-removal (DESIGN §4.2): clicking a row that is already in a slot
      // takes it out again, whichever slot holds it.
      if (state.a === action.id) {
        return assign(state, "a", null);
      }
      if (state.b === action.id) {
        return assign(state, "b", null);
      }
      return assign(state, state.armed, action.id);
    }

    case "set": {
      if (state[action.side] === action.id) {
        return assign(state, action.side, null);
      }
      return assign(state, action.side, action.id);
    }

    case "clear":
      return assign(state, action.side, null);
  }
}

/** Which slot, if any, currently holds this image. */
export function slotOf(state: SlotState, id: string): Side | null {
  if (state.a === id) {
    return "a";
  }
  if (state.b === id) {
    return "b";
  }
  return null;
}

export function bothFilled(state: SlotState): state is SlotState & { a: string; b: string } {
  return state.a !== null && state.b !== null;
}

/** Selecting one image for both slots is legal — it is the all-shared self-diff. */
export function sameImage(state: SlotState): boolean {
  return state.a !== null && state.a === state.b;
}
