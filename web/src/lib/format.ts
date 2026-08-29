const BINARY_UNITS = ["B", "KiB", "MiB", "GiB", "TiB", "PiB"] as const;

/**
 * Formats a byte count the way the UI shows sizes (DESIGN §2.1: mono, one
 * decimal place above KiB, e.g. `14.3 MiB`). The wire format is always raw
 * integers; humanization happens here.
 */
export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes)) {
    return "—";
  }
  const negative = bytes < 0;
  let value = Math.abs(bytes);
  let unit = 0;
  while (value >= 1024 && unit < BINARY_UNITS.length - 1) {
    value /= 1024;
    unit += 1;
  }
  const rendered =
    unit === 0 ? String(Math.round(value)) : value.toFixed(value < 10 ? 1 : value < 100 ? 1 : 0);
  return `${negative ? "-" : ""}${rendered} ${BINARY_UNITS[unit]}`;
}
