import { describe, expect, it } from "vitest";

import {
  branchSpinePaths,
  couldBeSharedEdge,
  forkPath,
  selectionRuleBox,
  trunkSpinePath,
} from "./geometry";
import type { Box } from "./geometry";

const card = (y: number, x = 0, w = 370, h = 60): Box => ({ x, y, w, h });

describe("trunkSpinePath", () => {
  it("joins the bottom of the first card to the top of the last", () => {
    expect(trunkSpinePath([card(0), card(74), card(148)])).toBe("M185 60 V148");
  });

  it("draws nothing when there is fewer than one gap to bridge", () => {
    expect(trunkSpinePath([])).toBeNull();
    expect(trunkSpinePath([card(0)])).toBeNull();
  });
});

describe("forkPath", () => {
  it("leaves the trunk downward and arrives at the branch downward", () => {
    // Control points sit 22px below the start and 22px above the end, so both
    // ends of the elbow are vertical and the curve reads as one continuous
    // flow rather than a diagonal.
    expect(forkPath(card(100), card(240, 0, 175))).toBe(
      "M185 160 C 185 182, 87.5 218, 87.5 240",
    );
  });

  it("bends toward the right-hand column when the branch is to the right", () => {
    expect(forkPath(card(100), card(240, 195, 175))).toBe(
      "M185 160 C 185 182, 282.5 218, 282.5 240",
    );
  });
});

describe("branchSpinePaths", () => {
  it("emits one vertical segment per consecutive pair", () => {
    expect(branchSpinePaths([card(0, 0, 175), card(100, 0, 175), card(200, 0, 175)])).toEqual([
      "M87.5 60 V100",
      "M87.5 160 V200",
    ]);
  });

  it("emits nothing for a single card", () => {
    expect(branchSpinePaths([card(0)])).toEqual([]);
  });
});

describe("couldBeSharedEdge", () => {
  const left = card(200, 0, 175, 60);
  const right = card(240, 195, 175, 60);

  it("runs from the right edge of the left card to the left edge of the right card", () => {
    expect(couldBeSharedEdge(left, right).d).toBe(
      "M175 230 C 197 186, 173 186, 195 270",
    );
  });

  it("bows above the higher of the two cards so the pill has clear background", () => {
    const { pillY } = couldBeSharedEdge(left, right);
    expect(pillY).toBe(Math.min(left.y, right.y) - 14);
    expect(pillY).toBeLessThan(left.y);
    expect(pillY).toBeLessThan(right.y);
  });

  it("centres the pill horizontally between the two cards", () => {
    expect(couldBeSharedEdge(left, right).pillX).toBe(185);
  });

  it("is symmetric in the vertical offset regardless of which card is higher", () => {
    const swapped = couldBeSharedEdge(card(240, 0, 175), card(200, 195, 175));
    expect(swapped.pillY).toBe(186);
  });
});

describe("selectionRuleBox", () => {
  it("sits just under the selected card and spans exactly its width", () => {
    expect(selectionRuleBox(card(200, 12, 175, 60))).toEqual({
      top: 266,
      left: 12,
      width: 175,
    });
  });
});
