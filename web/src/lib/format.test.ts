import { describe, expect, it } from "vitest";

import {
  formatByteDelta,
  formatBytes,
  formatCompactCount,
  formatRelativeTime,
  formatSoftEta,
  formatThroughput,
  shortDigest,
  shortHex,
} from "./format";

describe("formatBytes", () => {
  it.each([
    [0, "0 B"],
    [1, "1 B"],
    [1023, "1023 B"],
    [1024, "1.0 KiB"],
    [1536, "1.5 KiB"],
    [1024 * 1024, "1.0 MiB"],
    [15_000_000, "14.3 MiB"],
    [1024 ** 3, "1.0 GiB"],
    [1024 ** 4, "1.0 TiB"],
    [1024 ** 5, "1.0 PiB"],
    [-2048, "-2.0 KiB"],
  ])("formats %i bytes as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });

  it("renders an em dash for non-finite input", () => {
    expect(formatBytes(Number.NaN)).toBe("—");
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe("—");
  });

  describe("unit boundaries", () => {
    // The value just below a boundary must never render as "1024 <unit>":
    // rounding carries it into the next unit instead.
    it.each([
      [1023, "1023 B"],
      [1024, "1.0 KiB"],
      [1024 * 1024 - 1, "1.0 MiB"],
      [1024 * 1024, "1.0 MiB"],
      [1024 ** 3 - 1, "1.0 GiB"],
      [1024 ** 3, "1.0 GiB"],
      [1024 ** 4 - 1, "1.0 TiB"],
      [1024 ** 5 - 1, "1.0 PiB"],
      // 1023.5 MiB rounds to 1024 MiB at zero decimals, so it carries too.
      [Math.round(1023.5 * 1024 ** 2), "1.0 GiB"],
      // 1023.4 MiB stays put: it rounds to 1023, not 1024.
      [Math.round(1023.4 * 1024 ** 2), "1023 MiB"],
    ])("formats %i bytes as %s", (bytes, want) => {
      expect(formatBytes(bytes)).toBe(want);
    });

    it("never renders 1024 of any unit", () => {
      for (let unit = 1; unit <= 5; unit += 1) {
        for (const offset of [-2, -1, 0, 1, 2]) {
          expect(formatBytes(1024 ** unit + offset)).not.toMatch(/^1024 /);
        }
      }
    });
  });

  describe("precision rule (DESIGN 2.1: one decimal below 100, none at or above)", () => {
    it.each([
      [99.9 * 1024, "99.9 KiB"],
      [100 * 1024, "100 KiB"],
      [999 * 1024, "999 KiB"],
      [512 * 1024 ** 3, "512 GiB"],
    ])("formats %i bytes as %s", (bytes, want) => {
      expect(formatBytes(bytes)).toBe(want);
    });

    it("uses whole numbers for raw bytes", () => {
      expect(formatBytes(1)).toBe("1 B");
      expect(formatBytes(999)).toBe("999 B");
    });
  });

  it("signs negative values symmetrically", () => {
    for (const bytes of [1023, 1024, 15_000_000, 1024 ** 3 - 1]) {
      expect(formatBytes(-bytes)).toBe(`-${formatBytes(bytes)}`);
    }
  });
});

describe("formatByteDelta", () => {
  it.each([
    [14_985_591, "+14.3 MiB"],
    [-2_202_010, "−2.1 MiB"],
    [1, "+1 B"],
    [-1023, "−1023 B"],
    [0, "—"],
  ])("renders %i as %s", (bytes, expected) => {
    expect(formatByteDelta(bytes)).toBe(expected);
  });

  it("uses a real minus sign, not a hyphen", () => {
    expect(formatByteDelta(-1024).startsWith("−")).toBe(true);
  });
});

describe("formatCompactCount", () => {
  it.each([
    [0, "0"],
    [999, "999"],
    [1000, "1.0K"],
    [1200, "1.2K"],
    [999_949, "999.9K"],
    [9_900_000, "9.9M"],
    [-1200, "−1.2K"],
  ])("renders %i as %s", (count, expected) => {
    expect(formatCompactCount(count)).toBe(expected);
  });

  it("carries into the next unit rather than rendering 1000 of one", () => {
    // 999,999 rounds to 1000.0K at one decimal, which must promote to M.
    expect(formatCompactCount(999_999)).toBe("1.0M");
  });
});

describe("shortDigest", () => {
  const digest = `sha256:${"ab34c56".padEnd(60, "0")}9f21`;

  it("keeps the algorithm, the head and the tail", () => {
    expect(shortDigest(digest)).toBe("sha256:ab34c56…9f21");
  });

  it("leaves a digest that already fits alone", () => {
    expect(shortDigest("sha256:abcd")).toBe("sha256:abcd");
  });

  it("drops the algorithm prefix for tight columns", () => {
    expect(shortHex(digest)).toBe("ab34…9f21");
  });
});

describe("formatRelativeTime", () => {
  const now = new Date("2026-08-29T12:00:00Z");

  it.each([
    ["2026-08-29T10:00:00Z", "2 hours ago"],
    ["2026-08-28T12:00:00Z", "yesterday"],
    ["2026-08-26T12:00:00Z", "3 days ago"],
  ])("renders %s as %s", (iso, expected) => {
    expect(formatRelativeTime(iso, now)).toBe(expected);
  });

  it("does not invent a time for an unparseable stamp", () => {
    expect(formatRelativeTime("not-a-date", now)).toBe("unknown");
  });
});

describe("formatThroughput", () => {
  it("renders a rate in the same binary units as every other size", () => {
    expect(formatThroughput(40_054_784)).toBe("38.2 MiB/s");
    expect(formatThroughput(0)).toBe("0 B/s");
  });

  it("refuses to render a nonsense rate", () => {
    expect(formatThroughput(Number.NaN)).toBe("—");
    expect(formatThroughput(-1)).toBe("—");
  });
});

describe("formatSoftEta", () => {
  it.each([
    [12, "less than a minute left"],
    [59, "less than a minute left"],
    [245, "about 4 min left"],
    [3600, "about 1 hr left"],
    [9000, "about 3 hr left"],
  ])("phrases %d seconds softly", (seconds, expected) => {
    expect(formatSoftEta(seconds)).toBe(expected);
  });

  it("says nothing rather than guessing from a nonsense number", () => {
    expect(formatSoftEta(Number.POSITIVE_INFINITY)).toBe("");
  });
});
