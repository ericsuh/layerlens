/**
 * Client-side pre-flight mirror of the server's reference parse + allowlist
 * verdict (ARCHITECTURE §7.1), so the Registry input can say
 * "→ ghcr.io ✓ allowed" per keystroke before any request exists (DESIGN §4.3).
 *
 * **The server remains authoritative.** Nothing here is a security control:
 * `POST /api/v1/pulls` re-parses the reference and re-checks the allowlist
 * with `internal/imgref` before a socket is ever opened, and a disagreement
 * between the two is resolved by the server, always. This copy exists to make
 * the form answer instantly and to keep an obviously-doomed reference from
 * costing a round trip.
 *
 * The two implementations are pinned together by `testdata/refs.json`, read by
 * both `internal/imgref/imgref_test.go` and `refcheck.test.ts`, so they cannot
 * drift silently. The parse below therefore mirrors go-containerregistry's
 * `name` package rather than the (stricter, and different) distribution
 * grammar: same registry/repository split, same character sets, same Docker
 * Hub defaulting. Where it is knowingly coarser — exotic percent-escapes and
 * IPv6 literals in the registry position — it errs toward "invalid", which the
 * server would reject too, just with a different sentence.
 */

/**
 * The `imgref.DefaultPatterns` fallback, used only until `GET /api/v1/meta`
 * answers with the list the server is actually running. The operator can
 * change the allowlist, so the UI prefers the served copy and never treats
 * this one as the truth.
 */
export const DEFAULT_ALLOWED_PATTERNS: readonly string[] = [
  "docker.io",
  "index.docker.io",
  "registry-1.docker.io",
  "ghcr.io",
  "gcr.io",
  "*.gcr.io",
  "*.pkg.dev",
  "public.ecr.aws",
  "*.dkr.ecr.*.amazonaws.com",
  "*.azurecr.io",
];

export type RefVerdict =
  /** Nothing typed yet: not an error, and nothing to show (DESIGN §9 #7). */
  | { kind: "empty" }
  | { kind: "invalid" }
  | { kind: "not-allowed"; registry: string }
  | { kind: "ok"; registry: string; repository: string; tag?: string; digest?: string };

const DEFAULT_REGISTRY = "index.docker.io";
const DEFAULT_REGISTRY_ALIAS = "docker.io";
const DEFAULT_NAMESPACE = "library";
const DEFAULT_TAG = "latest";

// The three character sets go-containerregistry validates against. Uppercase
// is absent from the repository set on purpose: `ghcr.io/UPPER/img` names a
// repository no registry can serve, and accepting it here would mean showing a
// green verdict for a reference the server rejects.
const REPOSITORY_CHARS = /^[a-z0-9_\-./]+$/;
const TAG_CHARS = /^[A-Za-z0-9_\-.]+$/;
const CANONICAL_DIGEST = /^sha256:[0-9a-f]{64}$/;
// RFC 3986 reg-name, minus the delimiters handled separately below.
const REGISTRY_CHARS = /^[A-Za-z0-9._~!$&'()*+,;=%-]+$/;

interface ParsedRef {
  registry: string;
  repository: string;
  tag?: string;
  digest?: string;
}

/**
 * Normalizes the registry host the way the server does before it consults the
 * allowlist: DNS is case-insensitive and a trailing root dot names the same
 * host, so `GHCR.IO` and `ghcr.io.` must not be two different verdicts.
 *
 * Returns null for a host the server would refuse outright. An explicit port
 * is one of those: the allowlist names registries, and `ghcr.io:8443` is a
 * different service from the one the operator vetted.
 */
function normalizeRegistry(raw: string): string | null {
  if (raw === "") {
    return DEFAULT_REGISTRY;
  }
  // "@" is userinfo, "[" opens an IPv6 literal, ":" is a port — none of which
  // any allowlisted registry is ever spelled with.
  if (raw.includes("@") || raw.includes(":") || raw.startsWith("[")) {
    return null;
  }
  if (!REGISTRY_CHARS.test(raw)) {
    return null;
  }
  // The alias rewrite is case-sensitive upstream, so it happens before the
  // lowercasing rather than after it.
  const rewritten = raw === DEFAULT_REGISTRY_ALIAS ? DEFAULT_REGISTRY : raw;
  return rewritten.toLowerCase().replace(/\.$/, "");
}

/**
 * Splits `registry/repository`, using the upstream rule: the first segment is
 * the registry only when it is `localhost` or contains a dot or a colon —
 * which is why `library/alpine` is a Docker Hub repository and not a host.
 */
function parseRepository(name: string): ParsedRef | null {
  if (name === "") {
    return null;
  }
  let registry = "";
  let repository = name;
  const slash = name.indexOf("/");
  if (slash !== -1) {
    const head = name.slice(0, slash);
    if (head === "localhost" || head.includes(".") || head.includes(":")) {
      registry = head;
      repository = name.slice(slash + 1);
    }
  }
  if (repository.length > 255 || !REPOSITORY_CHARS.test(repository)) {
    return null;
  }
  const host = normalizeRegistry(registry);
  if (host === null) {
    return null;
  }
  return { registry: host, repository };
}

/** Docker Hub's implicit `library/` namespace, applied only for Hub itself. */
function repositoryStr(ref: ParsedRef): string {
  if (!ref.repository.includes("/") && ref.registry === DEFAULT_REGISTRY) {
    return `${DEFAULT_NAMESPACE}/${ref.repository}`;
  }
  return ref.repository;
}

function parseTag(name: string): ParsedRef | null {
  const parts = name.split(":");
  const last = parts[parts.length - 1] ?? "";
  let base = name;
  let tag = "";
  // A trailing ":something" is a tag only when it holds no "/" — otherwise it
  // is the port half of a `host:port/repo` reference, which the registry
  // normalization above then refuses.
  if (parts.length > 1 && !last.includes("/")) {
    base = parts.slice(0, -1).join(":");
    tag = last;
    if (tag === "" || tag.length > 128 || !TAG_CHARS.test(tag)) {
      return null;
    }
  }
  const repo = parseRepository(base);
  if (repo === null) {
    return null;
  }
  return { ...repo, tag: tag === "" ? DEFAULT_TAG : tag };
}

function parseDigest(name: string): ParsedRef | null {
  const parts = name.split("@");
  if (parts.length !== 2) {
    return null;
  }
  const [base, digest] = parts as [string, string];
  if (!CANONICAL_DIGEST.test(digest)) {
    return null;
  }
  // `repo:tag@digest` is legal and the tag is dropped: the digest is the
  // identity. Upstream does that by round-tripping the base through the tag
  // parser, which also applies Hub's implicit namespace — mirrored here so
  // `alpine@sha256:…` resolves to `library/alpine` on both sides.
  const asTag = parseTag(base);
  const canonical = asTag === null ? base : `${asTag.registry}/${repositoryStr(asTag)}`;
  const repo = parseRepository(canonical);
  if (repo === null) {
    return null;
  }
  return { ...repo, digest };
}

/**
 * Matches a label-wise host pattern where `*` consumes one or more **whole**
 * dot-separated labels.
 *
 * Backtracking rather than a regexp, for the same reason the server does it
 * this way: there is no way for a `*` to eat half a label, which is exactly
 * the bug that lets `evilgcr.io` slip past a naive suffix check on `*.gcr.io`.
 */
function matchLabels(pattern: readonly string[], labels: readonly string[]): boolean {
  if (pattern.length === 0) {
    return labels.length === 0;
  }
  const [head, ...rest] = pattern as [string, ...string[]];
  if (head === "*") {
    for (let take = 1; take <= labels.length; take += 1) {
      if (matchLabels(rest, labels.slice(take))) {
        return true;
      }
    }
    return false;
  }
  if (labels.length === 0 || labels[0] !== head) {
    return false;
  }
  return matchLabels(rest, labels.slice(1));
}

/** Whether a normalized registry host is on the allowlist. */
export function isAllowedRegistry(host: string, patterns: readonly string[]): boolean {
  if (host === "") {
    return false;
  }
  const labels = host.split(".");
  if (labels.some((label) => label === "")) {
    return false;
  }
  return patterns.some((pattern) => {
    const normalized = pattern.trim().toLowerCase();
    return normalized !== "" && matchLabels(normalized.split("."), labels);
  });
}

/**
 * The whole pre-flight verdict for one reference. Pure: the pattern list is an
 * argument so the caller can pass the list `/api/v1/meta` reported and the
 * shared-vector test can pass the server's defaults.
 */
export function checkReference(
  raw: string,
  patterns: readonly string[] = DEFAULT_ALLOWED_PATTERNS,
): RefVerdict {
  const trimmed = raw.trim();
  if (trimmed === "") {
    return { kind: "empty" };
  }
  // Tag first, then digest — the upstream order, and the one that makes
  // `repo@sha256:…` (whose tag parse fails on the "@") resolve correctly.
  const parsed = parseTag(trimmed) ?? parseDigest(trimmed);
  if (parsed === null) {
    return { kind: "invalid" };
  }
  if (!isAllowedRegistry(parsed.registry, patterns)) {
    return { kind: "not-allowed", registry: parsed.registry };
  }
  return {
    kind: "ok",
    registry: parsed.registry,
    repository: repositoryStr(parsed),
    ...(parsed.tag === undefined ? {} : { tag: parsed.tag }),
    ...(parsed.digest === undefined ? {} : { digest: parsed.digest }),
  };
}

/**
 * Product names for the allowlist, for the DESIGN §9 #8 sentence
 * ("Allowed: Docker Hub, GHCR, GCR, ECR, ACR").
 *
 * Derived from the patterns the server reports rather than hardcoded, so an
 * operator who narrows the allowlist does not get a message that names
 * registries their server will refuse. A pattern with no product name is
 * shown as itself, which is honest if unlovely.
 */
const REGISTRY_PRODUCT_NAMES: readonly [string, string][] = [
  ["docker.io", "Docker Hub"],
  ["index.docker.io", "Docker Hub"],
  ["registry-1.docker.io", "Docker Hub"],
  ["ghcr.io", "GHCR"],
  ["gcr.io", "GCR"],
  ["*.gcr.io", "GCR"],
  ["*.pkg.dev", "GCR"],
  ["public.ecr.aws", "ECR"],
  ["*.dkr.ecr.*.amazonaws.com", "ECR"],
  ["*.azurecr.io", "ACR"],
];

export function friendlyRegistryNames(patterns: readonly string[]): string[] {
  const names: string[] = [];
  for (const pattern of patterns) {
    const normalized = pattern.trim().toLowerCase();
    if (normalized === "") {
      continue;
    }
    const known = REGISTRY_PRODUCT_NAMES.find(([p]) => p === normalized);
    const name = known?.[1] ?? normalized;
    if (!names.includes(name)) {
      names.push(name);
    }
  }
  return names;
}
