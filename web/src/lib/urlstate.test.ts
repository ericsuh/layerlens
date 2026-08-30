import { describe, expect, it } from "vitest";

import {
  buildCompareSearch,
  compareHref,
  isImageId,
  parseCompareSearch,
  resolveLayerCounts,
} from "./urlstate";
import type { CompareUrlState } from "./urlstate";

const LEFT = `sha256:${"a".repeat(64)}`;
const RIGHT = `sha256:${"b".repeat(64)}`;

const FULL: CompareUrlState = {
  left: LEFT,
  right: RIGHT,
  l: 5,
  r: 6,
  path: "/app/node_modules",
  filter: "all",
};

describe("parseCompareSearch / buildCompareSearch", () => {
  it("round-trips every field", () => {
    expect(parseCompareSearch(buildCompareSearch(FULL))).toEqual(FULL);
  });

  it("keeps digests and paths readable rather than percent-encoded", () => {
    const search = buildCompareSearch(FULL);
    expect(search).toContain(`left=${LEFT}`);
    expect(search).toContain("path=/app/node_modules");
    expect(search).not.toContain("%3A");
    expect(search).not.toContain("%2F");
  });

  it("omits defaults so the common link stays short", () => {
    expect(
      buildCompareSearch({ left: LEFT, right: RIGHT, l: null, r: null, path: "/", filter: "changed" }),
    ).toBe(`?left=${LEFT}&right=${RIGHT}`);
  });

  it("builds the /compare href", () => {
    expect(compareHref(FULL).startsWith("/compare?")).toBe(true);
  });

  it("defaults l and r to null when absent, so the caller can apply full stacks", () => {
    const state = parseCompareSearch(`?left=${LEFT}&right=${RIGHT}`);
    expect(state.l).toBeNull();
    expect(state.r).toBeNull();
    expect(state.path).toBe("/");
    expect(state.filter).toBe("changed");
  });

  it("tolerates a leading question mark or its absence", () => {
    expect(parseCompareSearch(`left=${LEFT}`).left).toBe(LEFT);
    expect(parseCompareSearch(`?left=${LEFT}`).left).toBe(LEFT);
  });

  describe("malformed values fall back safely", () => {
    it.each([
      ["not-a-digest", "left"],
      ["sha256:zzzz", "left"],
      [`${LEFT}extra`, "left"],
    ])("rejects %s as an image id", (value) => {
      expect(parseCompareSearch(`?left=${value}`).left).toBeNull();
    });

    it.each(["-1", "1.5", "abc", "1e3", "+5", " 5"])("rejects %s as a layer count", (value) => {
      expect(parseCompareSearch(`?l=${encodeURIComponent(value)}`).l).toBeNull();
    });

    it("accepts zero, which is a real selection (nothing applied yet)", () => {
      expect(parseCompareSearch("?l=0").l).toBe(0);
    });

    it("falls back to the root for a relative or empty path", () => {
      expect(parseCompareSearch("?path=app").path).toBe("/");
      expect(parseCompareSearch("?path=").path).toBe("/");
    });

    it("normalizes redundant separators so one directory is one key", () => {
      expect(parseCompareSearch("?path=//app//lib/").path).toBe("/app/lib");
    });

    it("falls back to the default filter for an unknown value", () => {
      expect(parseCompareSearch("?filter=weird").filter).toBe("changed");
    });
  });

  it("carries phase-007's tree params through untouched", () => {
    // Nothing reads path/filter yet, but a link written today must keep
    // working when the tree lands, so the codec may not drop them.
    const state = parseCompareSearch(`?left=${LEFT}&right=${RIGHT}&path=/app&filter=all`);
    expect(buildCompareSearch(state)).toContain("path=/app");
    expect(buildCompareSearch(state)).toContain("filter=all");
  });
});

describe("isImageId", () => {
  it.each([
    [LEFT, true],
    [`sha256:${"A".repeat(64)}`, false],
    [`sha512:${"a".repeat(64)}`, false],
    ["", false],
  ])("classifies %s", (value, expected) => {
    expect(isImageId(value)).toBe(expected);
  });
});

describe("resolveLayerCounts", () => {
  const bounds = { leftLength: 8, rightLength: 9 };

  it("defaults a missing selection to the full stack on each side", () => {
    expect(resolveLayerCounts({ l: null, r: null }, bounds)).toEqual({ l: 8, r: 9 });
  });

  it("keeps an in-range selection", () => {
    expect(resolveLayerCounts({ l: 3, r: 7 }, bounds)).toEqual({ l: 3, r: 7 });
  });

  it("clamps an out-of-range selection from a stale link instead of rendering nothing", () => {
    expect(resolveLayerCounts({ l: 99, r: -4 }, bounds)).toEqual({ l: 8, r: 0 });
  });
});
