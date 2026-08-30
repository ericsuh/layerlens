import { useEffect, useMemo, useRef } from "react";
import {
  keepPreviousData,
  useInfiniteQuery,
  useMutation,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import type { InfiniteData } from "@tanstack/react-query";

import {
  cancelPull,
  fetchDockerImages,
  fetchImages,
  fetchLayerGraph,
  fetchMeta,
  fetchPull,
  fetchPulls,
  fetchTreePage,
  startPull,
} from "./client";
import type { ApiError } from "./client";
import type {
  DockerListing,
  ImagesResponse,
  LayerGraph,
  Meta,
  PullList,
  PullStatus,
  StartPullRequest,
  TreePage,
} from "./types";

/**
 * Query keys mirror the API params exactly (ARCHITECTURE §8.2), so a key can
 * always be read back as the request it stands for.
 */
export const queryKeys = {
  images: () => ["images"] as const,
  meta: () => ["meta"] as const,
  dockerImages: () => ["docker-images"] as const,
  pulls: () => ["pulls"] as const,
  pull: (id: string) => ["pull", id] as const,
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
 * The §6.3 polling cadence. The contract says 500–1000 ms while a pull is
 * resolving or running; 800 ms is inside it and, at the server's 100 ms
 * progress throttle, still shows every meaningful byte movement.
 */
export const PULL_POLL_MS = 800;

/** The two states that are still moving — the only ones worth polling. */
export function isPullActive(pull: PullStatus): boolean {
  return pull.state === "resolving" || pull.state === "running";
}

/**
 * `['images']` is invalidated **once** per pull that lands, which is the whole
 * mechanism that makes a finished pull show up in the Analyzed tab (§8.2).
 *
 * "Once" matters: a `done` pull stays `done` in every later poll, so
 * invalidating on the state alone would refetch the image list forever at the
 * poll interval. The set of already-reported ids is a ref rather than state
 * because it must not itself cause a render.
 *
 * `['docker-images']` goes with it: every row of that listing carries an
 * `alreadyAnalyzed`/`analyzedId` cross-reference into exactly the records a
 * finished pull just changed, so leaving it alone would keep offering an
 * `Analyze` button for an image that has just been analyzed.
 */
function useImagesInvalidationOnDone(pulls: readonly PullStatus[]): void {
  const client = useQueryClient();
  const reported = useRef<Set<string>>(new Set());
  useEffect(() => {
    const landed = pulls.filter(
      (pull) => pull.state === "done" && !reported.current.has(pull.id),
    );
    if (landed.length === 0) {
      return;
    }
    for (const pull of landed) {
      reported.current.add(pull.id);
    }
    void client.invalidateQueries({ queryKey: queryKeys.images() });
    void client.invalidateQueries({ queryKey: queryKeys.dockerImages() });
  }, [pulls, client]);
}

/** `['meta']`: the server version and the registry allowlist the UI names. */
export function useMetaQuery() {
  return useQuery<Meta>({
    queryKey: queryKeys.meta(),
    queryFn: ({ signal }) => fetchMeta(signal),
    staleTime: 60_000,
  });
}

/**
 * `['docker-images']` (§8.2). Listing the daemon costs an ImageList plus one
 * inspect per image and no transfer, but it is still a socket round trip per
 * mount, so it holds for 15 s; a refetch on window focus catches the image the
 * user just built in another terminal.
 */
export function useDockerImagesQuery() {
  return useQuery<DockerListing>({
    queryKey: queryKeys.dockerImages(),
    queryFn: ({ signal }) => fetchDockerImages(signal),
    staleTime: 15_000,
    retry: false,
  });
}

/**
 * `['pulls']` (§8.2): polls only while something is actually moving, so an
 * idle selection view makes no requests at all.
 */
export function usePullsQuery() {
  const query = useQuery<PullList>({
    queryKey: queryKeys.pulls(),
    queryFn: ({ signal }) => fetchPulls(signal),
    refetchInterval: (q) =>
      (q.state.data?.pulls ?? []).some(isPullActive) ? PULL_POLL_MS : false,
    retry: false,
  });
  const pulls = useMemo(() => query.data?.pulls ?? [], [query.data]);
  useImagesInvalidationOnDone(pulls);
  return query;
}

/**
 * `['pull', id]` (§8.2): the same cadence for one pull, stopping dead on every
 * terminal state — `done`, `error` and `cancelled` never change again, so a
 * further poll could only ever return what the client already has.
 */
export function usePullQuery(id: string | null) {
  const query = useQuery<PullStatus>({
    queryKey: queryKeys.pull(id ?? ""),
    queryFn: ({ signal }) => fetchPull(id ?? "", signal),
    enabled: id !== null,
    refetchInterval: (q) =>
      q.state.data !== undefined && isPullActive(q.state.data) ? PULL_POLL_MS : false,
    retry: false,
  });
  const pulls = useMemo(() => (query.data === undefined ? [] : [query.data]), [query.data]);
  useImagesInvalidationOnDone(pulls);
  return query;
}

/**
 * `POST /pulls`. The response is written straight into `['pull', id]` so the
 * progress card renders from the 202 rather than waiting a poll interval for
 * its first GET, and `['pulls']` is invalidated so the card joins the list
 * above the tabs immediately.
 */
export function useStartPull() {
  const client = useQueryClient();
  return useMutation<PullStatus, ApiError, StartPullRequest>({
    mutationFn: (body) => startPull(body),
    onSuccess: (status) => {
      client.setQueryData(queryKeys.pull(status.id), status);
      void client.invalidateQueries({ queryKey: queryKeys.pulls() });
    },
  });
}

/** `DELETE /pulls/{id}` — the server answers with the cancelled status. */
export function useCancelPull() {
  const client = useQueryClient();
  return useMutation<PullStatus, ApiError, string>({
    mutationFn: (id) => cancelPull(id),
    onSuccess: (status) => {
      client.setQueryData(queryKeys.pull(status.id), status);
      void client.invalidateQueries({ queryKey: queryKeys.pulls() });
    },
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
