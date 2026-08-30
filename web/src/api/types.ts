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

/** §6.2 — one image the local Docker daemon offers. */
export interface DockerImageSummary {
  /** `repo:tag` — exactly what `POST /pulls` accepts back. */
  reference: string;
  dockerId: string;
  /** The daemon's own uncompressed estimate, not a measured size. */
  sizeBytes: number;
  platform?: string;
  /** True when an analyzed record already exists, so selecting it is free. */
  alreadyAnalyzed: boolean;
  /** That record's image id — the id the A/B slots take. */
  analyzedId?: string;
}

/**
 * §6.2 — `GET /api/v1/docker/images`.
 *
 * "No Docker" is not an error anywhere in this API: a server with no socket
 * answers `available: false` with a `reason` written to be shown verbatim
 * (DESIGN state #4), because a missing daemon is a fact about the deployment
 * rather than a failed request.
 */
export interface DockerListing {
  available: boolean;
  reason?: string;
  images: DockerImageSummary[];
}

/** §6.3 — the body of `POST /api/v1/pulls`. */
export interface StartPullRequest {
  source: "registry" | "docker";
  reference: string;
}

export type PullSource = "registry" | "docker";

/** `resolving` and `running` are the two states that keep polling (§8.2). */
export type PullState = "resolving" | "running" | "done" | "error" | "cancelled";

export interface PullLayerProgress {
  index: number;
  digest: string;
  bytesDone: number;
  bytesTotal?: number;
}

/**
 * §6.3 — the polling payload for one ingest.
 *
 * `bytesTotal` is exact on the registry path (the manifest lists every layer's
 * compressed size up front) and absent on the daemon path, where
 * `bytesEstimated` is true and the UI must label the numbers as an estimate.
 */
export interface PullStatus {
  id: string;
  reference: string;
  source: PullSource;
  state: PullState;
  startedAt: string;
  bytesTotal?: number;
  bytesDone: number;
  bytesEstimated: boolean;
  layersTotal?: number;
  /** Includes layers skipped because they were already indexed. */
  layersDone: number;
  layersSkipped: number;
  currentLayer?: PullLayerProgress;
  /** Set when `state === "done"` — the id the A/B slots take. */
  imageId?: string;
  /** Set when `state === "error"`; `message` is shown verbatim (§6.1). */
  error?: { code: string; message: string };
}

export interface PullList {
  pulls: PullStatus[];
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

/**
 * §6.5 — one side's metadata for a tree row.
 *
 * `implicit` marks a directory no layer header ever named: it exists only
 * because a child needed a parent, so its `mode` is a value the server's
 * squashing invented. The UI renders "—" for those rather than a permission
 * string that is ours and not the image's. Absent means false.
 */
export interface TreeSideMeta {
  kind: "file" | "dir" | "symlink" | "hardlink" | "device" | "fifo";
  mode: number;
  sizeBytes: number;
  implicit?: boolean;
  linkTarget?: string;
}

/**
 * §6.5 — the subtree aggregate for a row.
 *
 * The four side totals are always present. The seven change breakdowns are
 * **omitted when zero**, so every reader must treat an absent field as 0 —
 * use `changeOf()` rather than reaching for the optional fields directly.
 * Deltas are derivable (`rightBytes - leftBytes`) and are not on the wire.
 */
export interface TreeAgg {
  leftBytes: number;
  rightBytes: number;
  leftFiles: number;
  rightFiles: number;
  addedBytes?: number;
  removedBytes?: number;
  modifiedBytesLeft?: number;
  modifiedBytesRight?: number;
  addedFiles?: number;
  removedFiles?: number;
  modifiedFiles?: number;
}

export type TreeStatus = "added" | "removed" | "modified" | "unchanged";

export interface TreeRow {
  name: string;
  path: string;
  status: TreeStatus;
  /** Absent ⇒ the entry is added (it exists only in image B). */
  left?: TreeSideMeta;
  /** Absent ⇒ the entry is removed (it exists only in image A). */
  right?: TreeSideMeta;
  agg: TreeAgg;
  hasChildren: boolean;
  /** Post-filter direct children — the honest `aria-setsize`. */
  childCount: number;
  /** Only on depth=2 responses: at most 50 per row, 2000 per response. */
  children?: TreeRow[];
  /**
   * True whenever children were cut — including cut to *none*, in which case
   * `children` is absent entirely. The remedy is the same either way: page
   * that directory with a request rooted at this row's path.
   */
  childrenTruncated?: boolean;
}

export interface TreePage {
  path: string;
  rows: TreeRow[];
  /** Absent ⇒ last page for this path+filter. */
  nextCursor?: string;
  totalRows: number;
  /** Denominator for the relative-size bars; stable across pages by contract. */
  maxSiblingBytes: number;
  pathStatus: TreeStatus;
  pathAgg: TreeAgg;
}
