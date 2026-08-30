import { describe, expect, it } from "vitest";

import { bothFilled, initialSlotState, sameImage, slotOf, slotReducer } from "./slots";
import type { SlotAction, SlotState } from "./slots";

const A = "sha256:aaa";
const B = "sha256:bbb";
const C = "sha256:ccc";

function run(actions: SlotAction[], from: SlotState = initialSlotState): SlotState {
  return actions.reduce(slotReducer, from);
}

describe("slotReducer", () => {
  it("arms slot A first", () => {
    expect(initialSlotState.armed).toBe("a");
  });

  it("fills A then B on consecutive plain clicks", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "pick", id: B },
    ]);
    expect(state).toMatchObject({ a: A, b: B });
  });

  it("arms the other slot while it is still empty", () => {
    expect(run([{ type: "pick", id: A }]).armed).toBe("b");
  });

  it("replaces the armed slot once both are filled", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "pick", id: B },
      { type: "pick", id: C },
    ]);
    // B was filled last, so B stays armed and takes the replacement.
    expect(state).toMatchObject({ a: A, b: C });
  });

  it("honours an explicit slot click before the next pick", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "pick", id: B },
      { type: "arm", side: "a" },
      { type: "pick", id: C },
    ]);
    expect(state).toMatchObject({ a: C, b: B });
  });

  it("places explicitly with Set A / Set B regardless of arming", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "set", side: "a", id: B },
    ]);
    expect(state).toMatchObject({ a: B });
  });

  it("removes an image when it is picked a second time", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "pick", id: A },
    ]);
    expect(state.a).toBeNull();
    expect(state.armed).toBe("a");
  });

  it("removes from whichever slot holds it, not just the armed one", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "pick", id: B },
      { type: "arm", side: "b" },
      { type: "pick", id: A },
    ]);
    expect(state.a).toBeNull();
    expect(state.b).toBe(B);
  });

  it("toggles off through Set A when the slot already holds that image", () => {
    const state = run([
      { type: "set", side: "a", id: A },
      { type: "set", side: "a", id: A },
    ]);
    expect(state.a).toBeNull();
  });

  it("clears a slot and arms it", () => {
    const state = run([
      { type: "pick", id: A },
      { type: "pick", id: B },
      { type: "clear", side: "a" },
    ]);
    expect(state).toMatchObject({ a: null, b: B, armed: "a" });
  });

  it("allows the same image in both slots — the all-shared self-diff", () => {
    const state = run([
      { type: "set", side: "a", id: A },
      { type: "set", side: "b", id: A },
    ]);
    expect(bothFilled(state)).toBe(true);
    expect(sameImage(state)).toBe(true);
  });
});

describe("slotOf", () => {
  const state = run([
    { type: "pick", id: A },
    { type: "pick", id: B },
  ]);

  it.each([
    [A, "a"],
    [B, "b"],
    [C, null],
  ])("reports the slot holding %s", (id, expected) => {
    expect(slotOf(state, id)).toBe(expected);
  });
});

describe("bothFilled", () => {
  it("gates the Compare button until both slots hold an image", () => {
    expect(bothFilled(initialSlotState)).toBe(false);
    expect(bothFilled(run([{ type: "pick", id: A }]))).toBe(false);
    expect(bothFilled(run([{ type: "pick", id: A }, { type: "pick", id: B }]))).toBe(true);
  });
});
