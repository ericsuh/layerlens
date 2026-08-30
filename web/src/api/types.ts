/**
 * Wire types for the layerlens HTTP API.
 *
 * Hand-written mirrors of ARCHITECTURE.md §6 and, field-for-field, of the Go
 * DTO structs in `internal/server/dto.go`. They are deliberately not generated:
 * the contract is small, and a hand-written mirror makes a wire change show up
 * as a compile error in the component that reads the field.
 */

/** §6.1 — the envelope every non-2xx response carries. */
export interface ApiErrorBody {
  error: {
    code: string;
    message: string;
    details?: Record<string, unknown>;
  };
}

/** §6.2 — one entry of `GET /api/v1/images`. */
export interface ImageSummary {
  id: string;
  refNames: string[];
  source: "fixture" | "registry" | "docker";
  platform: string;
  layerCount: number;
  totalBytes: number;
  createdAt: string;
  ingestedAt: string;
  pinned: boolean;
}

export interface ImagesResponse {
  images: ImageSummary[];
}

/** §6.4 — one layer of an image, ordered base → latest. */
export interface LayerInfo {
  index: number;
  diffId: string;
  chainId: string;
  compressedDigest?: string;
  compressedSize?: number;
  contentBytes: number;
  entryCount: number;
  instruction: string;
  instructionRaw: string;
  instructionKnown: boolean;
}

/** `owner: "shared"` means real layer-cache sharing — the trunk. */
export type LayerOwner = "shared" | "left" | "right";

export interface GraphLayer extends LayerInfo {
  owner: LayerOwner;
}

/**
 * A pair of post-fork layers with identical normalized content.
 *
 * These are **not** cache hits: the layers have different ChainIDs, so Docker
 * built both. The UI must never word them as sharing (RESEARCH Q9).
 */
export interface CouldBeSharedEdge {
  leftIndex: number;
  rightIndex: number;
  diffIdEqual: boolean;
}

export interface LayerGraph {
  left: ImageSummary;
  right: ImageSummary;
  trunkLength: number;
  trunk: GraphLayer[];
  leftBranch: GraphLayer[];
  rightBranch: GraphLayer[];
  couldBeShared: CouldBeSharedEdge[];
  maxLayerBytes: number;
}

/** §6.6 */
export interface Meta {
  version: string;
  cacheBytesUsed: number;
  cacheMaxBytes: number;
  allowedRegistries: string[];
}
