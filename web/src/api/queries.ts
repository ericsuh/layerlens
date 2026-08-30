import { keepPreviousData, useInfiniteQuery, useQuery } from "@tanstack/react-query";
import type { InfiniteData } from "@tanstack/react-query";

import { fetchImages, fetchLayerGraph, fetchTreePage } from "./client";
import type { ApiError } from "./client";
import type { ImagesResponse, LayerGraph, TreePage } from "./types";

/**
 * Query keys mirror the API params exactly (ARCHITECTURE §8.2), so a key can
 * always be read back as the request it stands for.
 */
export const queryKeys = {
  images: () => ["images"] as const,
  layerGraph: (left: string, right: string) => ["layer-graph", left, right] as const,
  /** The tuple is the request: same key ⇔ same URL (§8.2). */
  tree: (
    left: string,
    right: string,
    leftLayers: number,
    rightLayers: number,
    path: string,
    filter: "all" | "changed",
  ) => ["tree", left, right, leftLayers, rightLayers, path, filter] as const,
};

/** `['images']`: cheap, changes only when a pull lands. §8.2 policy: 30 s stale. */
export function useImagesQuery() {
  return useQuery<ImagesResponse>({
    queryKey: queryKeys.images(),
    queryFn: ({ signal }) => fetchImages(signal),
    staleTime: 30_000,
  });
}

/**
 * `['layer-graph', left, right]`: content-addressed by two config digests, so
 * the response can never change server-side — `staleTime: Infinity` and no
 * invalidation story at all (§8.2).
 */
export function useLayerGraphQuery(left: string | null, right: string | null) {
  return useQuery<LayerGraph>({
    queryKey: queryKeys.layerGraph(left ?? "", right ?? ""),
    queryFn: ({ signal }) => fetchLayerGraph(left ?? "", right ?? "", signal),
    enabled: left !== null && right !== null,
    staleTime: Infinity,
    retry: false,
  });
}

/**
 * `['tree', left, right, l, r, path, filter]` — one directory's children
 * (§8.2). Content-addressed like the layer graph: image ids are digests and a
 * layer selection is a count, so a key names one immutable answer.
 * `staleTime: Infinity` and no invalidation story.
 *
 * `placeholderData: keepPreviousData` is what makes DESIGN state #24 possible:
 * on a selection change the key changes, and the panel keeps rendering the old
 * rows (dimmed, with a progress bar) instead of flashing empty.
 */
export function useTreeDirectoryQuery(args: {
  left: string;
  right: string;
  leftLayers: number;
  rightLayers: number;
  path: string;
  filter: "all" | "changed";
  enabled: boolean;
}) {
  const { left, right, leftLayers, rightLayers, path, filter, enabled } = args;
  return useInfiniteQuery<TreePage, ApiError, InfiniteData<TreePage>, ReturnType<typeof queryKeys.tree>, string | undefined>({
    queryKey: queryKeys.tree(left, right, leftLayers, rightLayers, path, filter),
    queryFn: ({ pageParam, signal }) =>
      fetchTreePage(
        {
          left,
          right,
          leftLayers,
          rightLayers,
          path,
          filter,
          // depth=2 only on the first page: it is a prefetch of one level of
          // grandchildren, and re-requesting them for every page of a wide
          // directory would multiply the payload for nothing.
          ...(pageParam === undefined ? { depth: 2 as const } : { cursor: pageParam }),
        },
        signal,
      ),
    initialPageParam: undefined,
    getNextPageParam: (lastPage) => lastPage.nextCursor,
    enabled,
    staleTime: Infinity,
    gcTime: 5 * 60_000,
    placeholderData: keepPreviousData,
    // A cursor the server rejects is not retryable by repetition: the client
    // has to start the directory over. `resetOnStaleCursor` in the panel does
    // that; retrying here would just spend three more round trips first.
    retry: false,
  });
}
