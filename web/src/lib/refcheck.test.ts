import { readFileSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { describe, expect, it } from "vitest";

import { checkReference, DEFAULT_ALLOWED_PATTERNS, friendlyRegistryNames } from "./refcheck";

/**
 * The accept/reject table is a **shared** file: `internal/imgref/imgref_test.go`
 * reads the same JSON. That is the whole mechanism keeping the server's rule
 * and this client-side mirror from drifting — a vector added for one
 * implementation immediately runs against the other.
 */
interface AcceptVector {
  input: string;
  registry: string;
  repository: string;
  tag?: string;
  digest?: string;
}

interface RejectVector {
  input: string;
  reason: "invalid" | "not_allowed";
  registry?: string;
}

// Resolved through `fileURLToPath` rather than handed to `readFileSync` as a
// URL: the jsdom environment replaces the global `URL` class, and node's fs
// does not accept an instance of it.
const vectorsPath = resolve(
  dirname(fileURLToPath(import.meta.url)),
  "../../../testdata/refs.json",
);
const vectors = JSON.parse(readFileSync(vectorsPath, "utf8")) as {
  accept: AcceptVector[];
  reject: RejectVector[];
};

describe("checkReference against the shared server vectors", () => {
  it("has vectors to run", () => {
    expect(vectors.accept.length).toBeGreaterThan(10);
    expect(vectors.reject.length).toBeGreaterThan(10);
  });

  it.each(vectors.accept)("accepts $input", (vector) => {
    const verdict = checkReference(vector.input, DEFAULT_ALLOWED_PATTERNS);
    expect(verdict).toEqual({
      kind: "ok",
      registry: vector.registry,
      repository: vector.repository,
      ...(vector.tag === undefined ? {} : { tag: vector.tag }),
      ...(vector.digest === undefined ? {} : { digest: vector.digest }),
    });
  });

  it.each(vectors.reject)("rejects $input as $reason", (vector) => {
    const verdict = checkReference(vector.input, DEFAULT_ALLOWED_PATTERNS);
    if (vector.reason === "not_allowed") {
      expect(verdict).toEqual({ kind: "not-allowed", registry: vector.registry });
      return;
    }
    // "empty" is a client-only refinement of the server's `invalid`: the
    // server has no notion of "the user has not typed anything yet", but the
    // form must not show an error over an untouched input (DESIGN §4.3). It is
    // only ever reachable from a blank string, which the next case pins.
    expect(verdict.kind === "empty" ? "invalid" : verdict.kind).toBe("invalid");
    if (verdict.kind === "empty") {
      expect(vector.input.trim()).toBe("");
    }
  });
});

describe("checkReference", () => {
  it("reports an untyped input as empty, not as an error", () => {
    expect(checkReference("")).toEqual({ kind: "empty" });
    expect(checkReference("   ")).toEqual({ kind: "empty" });
  });

  it("honours a narrowed allowlist from the server", () => {
    // The operator's list, not ours: ghcr.io is on the default allowlist and
    // must still come back not-allowed when the server does not list it.
    expect(checkReference("ghcr.io/org/img", ["gcr.io"])).toEqual({
      kind: "not-allowed",
      registry: "ghcr.io",
    });
  });

  it("keeps a wildcard on whole label boundaries", () => {
    expect(checkReference("us.gcr.io/p/i", ["*.gcr.io"]).kind).toBe("ok");
    expect(checkReference("a.b.gcr.io/p/i", ["*.gcr.io"]).kind).toBe("ok");
    // The two shapes a naive suffix or substring check would let through.
    expect(checkReference("evilgcr.io/p/i", ["*.gcr.io"]).kind).toBe("not-allowed");
    expect(checkReference("gcr.io.evil.com/p/i", ["*.gcr.io"]).kind).toBe("not-allowed");
    // "*" is one or more labels, never zero.
    expect(checkReference("gcr.io/p/i", ["*.gcr.io"]).kind).toBe("not-allowed");
  });

  it("normalizes a trailing root dot away before the verdict", () => {
    expect(checkReference("ghcr.io./org/img")).toEqual({
      kind: "ok",
      registry: "ghcr.io",
      repository: "org/img",
      tag: "latest",
    });
  });
});

describe("friendlyRegistryNames", () => {
  it("names the default allowlist the way DESIGN §9 #8 words it", () => {
    expect(friendlyRegistryNames(DEFAULT_ALLOWED_PATTERNS)).toEqual([
      "Docker Hub",
      "GHCR",
      "GCR",
      "ECR",
      "ACR",
    ]);
  });

  it("falls back to the pattern itself for a host it has no product name for", () => {
    expect(friendlyRegistryNames(["registry.internal", "ghcr.io"])).toEqual([
      "registry.internal",
      "GHCR",
    ]);
  });
});
