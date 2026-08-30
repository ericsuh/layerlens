/**
 * The `/compare` URL codec (ARCHITECTURE §8.3).
 *
 * `/compare?left=sha256:aaa…&right=sha256:bbb…&l=5&r=6&path=/app&filter=changed`
 *
 * Every shareable piece of the comparison lives here: pasting the URL must
 * reproduce the exact view. `path` and `filter` belong to the filesystem tree
 * (phase 007) and nothing reads them yet — but they round-trip untouched so
 * that a link written today keeps working when the tree lands.
 */

export type TreeFilter = "all" | "changed";

export interface CompareUrlState {
  /** Image ids, or null when the param is missing or malformed. */
  left: string | null;
  right: string | null;
  /**
   * Selected layer *counts* (`leftLayers`/`rightLayers` in §6.5): n means
   * "layers 1..n included". null means "not specified" — the caller applies
   * the full-stack default once it knows how many layers each image has.
   */
  l: number | null;
  r: number | null;
  path: string;
  filter: TreeFilter;
}

export const DEFAULT_PATH = "/";
export const DEFAULT_FILTER: TreeFilter = "changed";

const DIGEST = /^sha256:[0-9a-f]{64}$/;

/** Image ids are full `sha256:<64 hex>` strings; anything else is not an id. */
export function isImageId(value: string): boolean {
  return DIGEST.test(value);
}

function parseLayerCount(value: string | null): number | null {
  if (value === null || value === "") {
    return null;
  }
  // Deliberately strict: "5x", "1e3", " 5", "+5" and "5.5" are all malformed
  // rather than silently coerced, so a hand-edited URL fails loudly into the
  // default instead of quietly selecting the wrong layer.
  if (!/^\d+$/.test(value)) {
    return null;
  }
  const n = Number(value);
  return Number.isSafeInteger(n) ? n : null;
}

function parsePath(value: string | null): string {
  if (value === null || value === "" || !value.startsWith("/")) {
    return DEFAULT_PATH;
  }
  // Collapse repeated separators and drop a trailing slash so that "/app/",
  // "//app" and "/app" are one cache key rather than three.
  const collapsed = value.replace(/\/{2,}/g, "/").replace(/\/+$/, "");
  return collapsed === "" ? DEFAULT_PATH : collapsed;
}

export function parseCompareSearch(search: string): CompareUrlState {
  const params = new URLSearchParams(search.startsWith("?") ? search.slice(1) : search);
  const left = params.get("left");
  const right = params.get("right");
  const filter = params.get("filter");
  return {
    left: left !== null && isImageId(left) ? left : null,
    right: right !== null && isImageId(right) ? right : null,
    l: parseLayerCount(params.get("l")),
    r: parseLayerCount(params.get("r")),
    path: parsePath(params.get("path")),
    filter: filter === "all" || filter === "changed" ? filter : DEFAULT_FILTER,
  };
}

/**
 * Serializes back to a search string. Defaults are omitted so the common URL
 * stays short and two equivalent views produce the same string.
 */
export function buildCompareSearch(state: CompareUrlState): string {
  const params = new URLSearchParams();
  if (state.left !== null) {
    params.set("left", state.left);
  }
  if (state.right !== null) {
    params.set("right", state.right);
  }
  if (state.l !== null) {
    params.set("l", String(state.l));
  }
  if (state.r !== null) {
    params.set("r", String(state.r));
  }
  if (state.path !== DEFAULT_PATH) {
    params.set("path", state.path);
  }
  if (state.filter !== DEFAULT_FILTER) {
    params.set("filter", state.filter);
  }
  const encoded = params.toString();
  // URLSearchParams percent-encodes ":" and "/", which are both legal in a
  // query value and are what make these URLs readable when pasted around.
  return encoded === "" ? "" : `?${encoded.replace(/%3A/g, ":").replace(/%2F/g, "/")}`;
}

/** The href for a comparison, including the route. */
export function compareHref(state: CompareUrlState): string {
  return `/compare${buildCompareSearch(state)}`;
}

export interface LayerBounds {
  leftLength: number;
  rightLength: number;
}

/**
 * Resolves `l`/`r` against the real layer counts: missing means the full stack
 * (the most useful entry view, DESIGN §5.2), and an out-of-range value from a
 * hand-edited or stale URL clamps into range rather than rendering nothing.
 */
export function resolveLayerCounts(
  state: Pick<CompareUrlState, "l" | "r">,
  bounds: LayerBounds,
): { l: number; r: number } {
  const clamp = (value: number | null, max: number): number => {
    if (value === null) {
      return max;
    }
    return Math.min(Math.max(value, 0), max);
  };
  return { l: clamp(state.l, bounds.leftLength), r: clamp(state.r, bounds.rightLength) };
}
