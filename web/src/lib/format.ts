const BINARY_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/** Bytes are rendered whole; every larger unit gets one decimal below 100. */
function fractionDigits(unit: number, value: number): number {
  if (unit === 0) {
    return 0;
  }
  return value < 100 ? 1 : 0;
}

/**
 * Formats a byte count the way the UI shows sizes (DESIGN §2.1: binary units,
 * one decimal below 100, none at or above it — `14.3 MiB`, `999 KiB`,
 * `1.0 GiB`). The wire format is always raw integers; humanization happens
 * here.
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes)) {
    return "—";
  }
  const negative = bytes < 0;
  let value = Math.abs(bytes);
  let unit = 0;

  // Promote on the *rounded* value, not the raw one. 1048575 B is 1023.999…
  // KiB, which rounds to "1024 KiB" — a unit that should have carried into
  // "1.0 MiB". Rounding first makes the carry visible to the loop.
  while (unit < BINARY_UNITS.length - 1) {
    if (Number(value.toFixed(fractionDigits(unit, value))) < 1024) {
      break;
    }
    value /= 1024;
    unit += 1;
  }

  const rendered = value.toFixed(fractionDigits(unit, value));
  return `${negative ? "-" : ""}${rendered} ${BINARY_UNITS[unit]}`;
}

/**
 * A signed byte delta, always carrying its sign (DESIGN §3): `+14.3 MiB`,
 * `−2.1 MiB`. The minus is U+2212 MINUS SIGN, not a hyphen, so the two
 * polarities have the same optical weight in a tabular-nums column.
 */
export function formatByteDelta(bytes: number): string {
  if (!Number.isFinite(bytes)) {
    return "—";
  }
  if (bytes === 0) {
    return "—";
  }
  const magnitude = formatBytes(Math.abs(bytes));
  return `${bytes > 0 ? "+" : "−"}${magnitude}`;
}

/**
 * Counts that must fit a fixed-width column (DESIGN §3): plain below 1000,
 * then one decimal with a K/M/G suffix, so `± 9.9M` is the widest rendering.
 */
export function formatCompactCount(count: number): string {
  if (!Number.isFinite(count)) {
    return "—";
  }
  const negative = count < 0;
  let value = Math.abs(count);
  const units = ["", "K", "M", "G"];
  let unit = 0;
  while (unit < units.length - 1 && Number(value.toFixed(unit === 0 ? 0 : 1)) >= 1000) {
    value /= 1000;
    unit += 1;
  }
  const rendered = unit === 0 ? String(Math.round(value)) : value.toFixed(1);
  return `${negative ? "−" : ""}${rendered}${units[unit]}`;
}

/** `1,204` — thousands-separated, for the counts that have room. */
export function formatCount(count: number): string {
  return count.toLocaleString("en-US");
}

const RELATIVE_UNITS: [limitSeconds: number, divisor: number, unit: Intl.RelativeTimeFormatUnit][] =
  [
    [60, 1, "second"],
    [3600, 60, "minute"],
    [86_400, 3600, "hour"],
    [2_592_000, 86_400, "day"],
    [31_536_000, 2_592_000, "month"],
    [Infinity, 31_536_000, "year"],
  ];

/**
 * "2 hours ago" for the analyzed-at column. Uses `Intl.RelativeTimeFormat` so
 * the phrasing is not hand-rolled; `now` is injectable to keep it testable.
 */
export function formatRelativeTime(iso: string, now: Date = new Date()): string {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) {
    return "unknown";
  }
  const deltaSeconds = (then - now.getTime()) / 1000;
  const magnitude = Math.abs(deltaSeconds);
  const format = new Intl.RelativeTimeFormat("en", { numeric: "auto" });
  for (const [limit, divisor, unit] of RELATIVE_UNITS) {
    if (magnitude < limit) {
      return format.format(Math.round(deltaSeconds / divisor), unit);
    }
  }
  return "unknown";
}

/**
 * Middle-truncates a `sha256:…` digest to `sha256:ab34c56…9f21` (DESIGN §3):
 * head *and* tail are the parts a human compares, so neither may be dropped.
 */
export function shortDigest(digest: string, head = 7, tail = 4): string {
  const colon = digest.indexOf(":");
  const algorithm = colon === -1 ? "" : digest.slice(0, colon + 1);
  const hex = colon === -1 ? digest : digest.slice(colon + 1);
  if (hex.length <= head + tail + 1) {
    return digest;
  }
  return `${algorithm}${hex.slice(0, head)}…${hex.slice(-tail)}`;
}

/** The same middle truncation with the algorithm prefix dropped, for tight columns. */
export function shortHex(digest: string, head = 4, tail = 4): string {
  const colon = digest.indexOf(":");
  const hex = colon === -1 ? digest : digest.slice(colon + 1);
  if (hex.length <= head + tail + 1) {
    return hex;
  }
  return `${hex.slice(0, head)}…${hex.slice(-tail)}`;
}
