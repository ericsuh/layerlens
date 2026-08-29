import { describe, expect, it } from "vitest";

import { formatBytes } from "./format";

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
    [-2048, "-2.0 KiB"],
  ])("formats %i bytes as %s", (bytes, want) => {
    expect(formatBytes(bytes)).toBe(want);
  });

  it("renders an em dash for non-finite input", () => {
    expect(formatBytes(Number.NaN)).toBe("—");
    expect(formatBytes(Number.POSITIVE_INFINITY)).toBe("—");
  });

  it("keeps three significant digits at large magnitudes", () => {
    expect(formatBytes(1024 ** 3 * 512)).toBe("512 GiB");
  });
});
