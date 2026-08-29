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
