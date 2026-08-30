import { useQuery } from "@tanstack/react-query";

import { fetchImages, fetchLayerGraph } from "./client";
import type { ImagesResponse, LayerGraph } from "./types";

/**
 * Query keys mirror the API params exactly (ARCHITECTURE §8.2), so a key can
 * always be read back as the request it stands for.
 */
export const queryKeys = {
  images: () => ["images"] as const,
  layerGraph: (left: string, right: string) => ["layer-graph", left, right] as const,
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
