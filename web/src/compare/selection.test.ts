import { describe, expect, it } from "vitest";

import {
  boundsOf,
  defaultSelection,
  isTrunkSelection,
  selectionLabel,
  selectionReducer,
} from "./selection";
import type { SelectionBounds } from "./selection";
import type { GraphLayer, LayerGraph } from "../api/types";

const BOUNDS: SelectionBounds = { trunkLength: 5, leftLength: 8, rightLength: 9 };

function layer(index: number): GraphLayer {
  return {
    index,
    diffId: `sha256:${String(index).repeat(4)}`,
    chainId: `sha256:c${String(index)}`,
    contentBytes: 1024,
    entryCount: 1,
    instruction: "RUN true",
    instructionRaw: "RUN /bin/sh -c true # buildkit",
    instructionKnown: true,
    owner: "shared",
  };
}

describe("defaultSelection", () => {
  it("starts on the full stack of each image — the full-image diff", () => {
    expect(defaultSelection(BOUNDS)).toEqual({ l: 8, r: 9 });
  });
});

describe("selectionReducer", () => {
  const start = defaultSelection(BOUNDS);

  it("makes a trunk selection set both sides, so the URL carries l === r", () => {
    const next = selectionReducer(start, { type: "select-trunk", count: 3 }, BOUNDS);
    expect(next).toEqual({ l: 3, r: 3 });
    expect(next.l).toBe(next.r);
  });

  it("moves only the left side for a left-branch selection", () => {
    expect(selectionReducer(start, { type: "select-left", count: 6 }, BOUNDS)).toEqual({
      l: 6,
      r: 9,
    });
  });

  it("moves only the right side for a right-branch selection", () => {
    expect(selectionReducer(start, { type: "select-right", count: 7 }, BOUNDS)).toEqual({
      l: 8,
      r: 7,
    });
  });

  it("clamps a trunk selection to the trunk, never past the fork", () => {
    expect(selectionReducer(start, { type: "select-trunk", count: 99 }, BOUNDS)).toEqual({
      l: 5,
      r: 5,
    });
  });

  it("guards out-of-range branch selections", () => {
    expect(selectionReducer(start, { type: "select-left", count: 99 }, BOUNDS).l).toBe(8);
    expect(selectionReducer(start, { type: "select-left", count: -3 }, BOUNDS).l).toBe(0);
    expect(selectionReducer(start, { type: "select-right", count: Number.NaN }, BOUNDS).r).toBe(9);
  });
});

describe("isTrunkSelection", () => {
  it.each([
    [{ l: 3, r: 3 }, true],
    [{ l: 3, r: 4 }, false],
    [{ l: 6, r: 6 }, false],
    [{ l: 0, r: 0 }, false],
  ])("classifies %j", (state, expected) => {
    expect(isTrunkSelection(state, BOUNDS)).toBe(expected);
  });
});

describe("boundsOf", () => {
  it("derives the per-side lengths from the graph's own arrays", () => {
    const graph: LayerGraph = {
      left: {
        id: "sha256:a",
        refNames: ["example:v1"],
        source: "fixture",
        platform: "linux/amd64",
        layerCount: 8,
        totalBytes: 1,
        createdAt: "",
        ingestedAt: "",
        pinned: true,
      },
      right: {
        id: "sha256:b",
        refNames: ["example:v2"],
        source: "fixture",
        platform: "linux/amd64",
        layerCount: 9,
        totalBytes: 1,
        createdAt: "",
        ingestedAt: "",
        pinned: true,
      },
      trunkLength: 5,
      trunk: [0, 1, 2, 3, 4].map(layer),
      leftBranch: [5, 6, 7].map(layer),
      rightBranch: [5, 6, 7, 8].map(layer),
      couldBeShared: [],
      maxLayerBytes: 1024,
    };
    expect(boundsOf(graph)).toEqual({ trunkLength: 5, leftLength: 8, rightLength: 9 });
  });
});

describe("selectionLabel", () => {
  it.each([
    ["a" as const, 6, "A @ layer 6"],
    ["b" as const, 7, "B @ layer 7"],
    ["a" as const, 0, "A @ base"],
  ])("labels %s at %i", (side, count, expected) => {
    expect(selectionLabel(side, count)).toBe(expected);
  });
});
