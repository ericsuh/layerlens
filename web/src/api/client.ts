import type { ApiErrorBody, ImagesResponse, LayerGraph, Meta, TreePage } from "./types";

/**
 * A non-2xx response, parsed out of the §6.1 envelope.
 *
 * `code` is the machine-readable kind the state table in DESIGN §9 branches on;
 * `message` is written by the server to be shown verbatim (§6.1 guarantees it
 * leaks no internals), so components render it directly rather than inventing
 * their own copy.
 */
export class ApiError extends Error {
  readonly code: string;
  readonly status: number;
  readonly details: Record<string, unknown> | undefined;

  constructor(status: number, code: string, message: string, details?: Record<string, unknown>) {
    super(message);
    this.name = "ApiError";
    this.status = status;
    this.code = code;
    this.details = details;
  }
}

function isErrorBody(value: unknown): value is ApiErrorBody {
  if (typeof value !== "object" || value === null || !("error" in value)) {
    return false;
  }
  const inner = value.error;
  return (
    typeof inner === "object" &&
    inner !== null &&
    typeof (inner as { code?: unknown }).code === "string" &&
    typeof (inner as { message?: unknown }).message === "string"
  );
}

/** Base URL of the API. Same origin always — the SPA is served by the server. */
const BASE = "/api/v1";

async function request<T>(path: string, signal?: AbortSignal): Promise<T> {
  let response: Response;
  try {
    response = await fetch(`${BASE}${path}`, {
      headers: { accept: "application/json" },
      ...(signal ? { signal } : {}),
    });
  } catch (cause) {
    // A transport failure is state #30: the SPA lost its backend. It gets its
    // own code so the UI can distinguish it from a server-sent error.
    throw new ApiError(0, "network_error", "Lost connection to the layerlens server.", {
      cause: String(cause),
    });
  }

  const text = await response.text();
  let parsed: unknown = undefined;
  if (text !== "") {
    try {
      parsed = JSON.parse(text);
    } catch {
      parsed = undefined;
    }
  }

  if (!response.ok) {
    if (isErrorBody(parsed)) {
      throw new ApiError(
        response.status,
        parsed.error.code,
        parsed.error.message,
        parsed.error.details,
      );
    }
    throw new ApiError(response.status, "internal", `Request failed (HTTP ${response.status}).`);
  }

  return parsed as T;
}

export function fetchImages(signal?: AbortSignal): Promise<ImagesResponse> {
  return request<ImagesResponse>("/images", signal);
}

export function fetchLayerGraph(
  left: string,
  right: string,
  signal?: AbortSignal,
): Promise<LayerGraph> {
  const params = new URLSearchParams({ left, right });
  return request<LayerGraph>(`/diff/layers?${params.toString()}`, signal);
}

export function fetchMeta(signal?: AbortSignal): Promise<Meta> {
  return request<Meta>("/meta", signal);
}

/** The `/diff/tree` query (§6.5), one directory's page of children. */
export interface TreeQuery {
  left: string;
  right: string;
  leftLayers: number;
  rightLayers: number;
  path: string;
  filter: "all" | "changed";
  /** Absent for the first page. */
  cursor?: string;
  /**
   * 2 asks the server to embed one level of grandchildren so expanding a row
   * costs no request (§8.4). Only ever sent on a first page: paging a wide
   * directory must not re-pay for grandchildren it already has.
   */
  depth?: 1 | 2;
  limit?: number;
}

export function fetchTreePage(query: TreeQuery, signal?: AbortSignal): Promise<TreePage> {
  // The server parses these strictly (no "+1", no leading zeros, path already
  // clean), so they are serialized exactly once, here.
  const params = new URLSearchParams({
    left: query.left,
    right: query.right,
    leftLayers: String(query.leftLayers),
    rightLayers: String(query.rightLayers),
    path: query.path,
    filter: query.filter,
  });
  if (query.depth !== undefined) {
    params.set("depth", String(query.depth));
  }
  if (query.limit !== undefined) {
    params.set("limit", String(query.limit));
  }
  if (query.cursor !== undefined && query.cursor !== "") {
    params.set("cursor", query.cursor);
  }
  return request<TreePage>(`/diff/tree?${params.toString()}`, signal);
}
