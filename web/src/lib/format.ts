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
 * Throughput on the pull progress card (DESIGN §4.4: "38.2 MiB/s"). Binary
 * units like every other size in the UI, so a rate and a total can be read
 * against each other without a mental conversion.
 */
export function formatThroughput(bytesPerSecond: number): string {
  if (!Number.isFinite(bytesPerSecond) || bytesPerSecond < 0) {
    return "—";
  }
  return `${formatBytes(bytesPerSecond)}/s`;
}

/**
 * A deliberately soft ETA (DESIGN §4.4: "about 4 min left").
 *
 * Rounded coarsely and worded vaguely on purpose: the estimate comes from a
 * few seconds of throughput on a link whose behaviour over the next 20 GiB
 * nobody knows, and a precise-looking "3 min 42 s left" would be claiming an
 * accuracy this number does not have.
 */
export function formatSoftEta(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds < 0) {
    return "";
  }
  if (seconds < 60) {
    return "less than a minute left";
  }
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) {
    return `about ${minutes} min left`;
  }
  const hours = Math.round(seconds / 3600);
  return `about ${hours} hr left`;
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

const UNIT_WORDS: Record<string, string> = {
  B: "bytes",
  KiB: "kibibytes",
  MiB: "mebibytes",
  GiB: "gibibytes",
  TiB: "tebibytes",
  PiB: "pebibytes",
};

/**
 * The same number a sighted user sees, with the unit spelled out for screen
 * readers (DESIGN §7: "14.3 mebibytes", not "14.3 MiB"). Abbreviations get
 * spelled letter by letter by most screen readers, which is unusable in a row
 * summary read aloud dozens of times.
 */
export function formatBytesSpoken(bytes: number): string {
  const rendered = formatBytes(bytes);
  const cut = rendered.lastIndexOf(" ");
  if (cut === -1) {
    return rendered;
  }
  const unit = rendered.slice(cut + 1);
  return `${rendered.slice(0, cut)} ${UNIT_WORDS[unit] ?? unit}`;
}

const MODE_BITS = ["r", "w", "x"] as const;

/**
 * `rwxr-xr-x` from the 12-bit permission word.
 *
 * Directories the server marks `implicit` never reach here: their mode is a
 * value squashing invented, not one the image carries, so the UI renders "—"
 * instead (ARCHITECTURE §6.5).
 */
export function formatMode(mode: number): string {
  let out = "";
  for (let group = 2; group >= 0; group -= 1) {
    for (let bit = 0; bit < 3; bit += 1) {
      const mask = 1 << (group * 3 + (2 - bit));
      out += (mode & mask) === 0 ? "-" : MODE_BITS[bit];
    }
  }
  return out;
}
