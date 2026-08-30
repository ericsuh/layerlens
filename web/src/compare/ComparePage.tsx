import { useCallback, useMemo } from "react";
import { useLocation, useSearch } from "wouter";

import { useLayerGraphQuery } from "../api/queries";
import { ApiError } from "../api/client";
import { ErrorPanel } from "../components/states";
import { buildCompareSearch, parseCompareSearch, resolveLayerCounts } from "../lib/urlstate";
import { FsDiffPanel } from "./FsDiffPanel";
import { LayerGraphPanel } from "./LayerGraphPanel";
import { LayerGraphSkeleton } from "./LayerGraphSkeleton";
import { boundsOf, selectionReducer } from "./selection";
import type { SelectionAction } from "./selection";

/**
 * The comparison view. All shareable state is read from and written back to
 * the URL (ARCHITECTURE §8.3), so this component holds no selection state of
 * its own: pasting a link and clicking a card go through exactly the same
 * path.
 */
export function ComparePage() {
  const search = useSearch();
  const [, navigate] = useLocation();
  const urlState = useMemo(() => parseCompareSearch(search), [search]);

  const query = useLayerGraphQuery(urlState.left, urlState.right);
  const graph = query.data;

  const bounds = useMemo(() => (graph === undefined ? null : boundsOf(graph)), [graph]);
  const selection = useMemo(
    () =>
      bounds === null
        ? null
        : resolveLayerCounts(urlState, {
            leftLength: bounds.leftLength,
            rightLength: bounds.rightLength,
          }),
    [bounds, urlState],
  );

  const updateUrl = useCallback(
    (next: Partial<typeof urlState>) => {
      // `replace`, like layer selection: drilling into a directory or
      // switching the filter refines the same view, and Back should leave the
      // comparison rather than replay every step inside it.
      navigate(`/compare${buildCompareSearch({ ...urlState, ...next })}`, { replace: true });
    },
    [navigate, urlState],
  );

  const dispatch = useCallback(
    (action: SelectionAction) => {
      if (bounds === null || selection === null) {
        return;
      }
      const next = selectionReducer(selection, action, bounds);
      // `replace`: adjusting the layer point is a refinement of the same view,
      // not a new destination — otherwise Back would walk every click.
      navigate(`/compare${buildCompareSearch({ ...urlState, l: next.l, r: next.r })}`, {
        replace: true,
      });
    },
    [bounds, navigate, selection, urlState],
  );

  if (urlState.left === null || urlState.right === null) {
    return (
      <ErrorPanel
        title="This comparison link is incomplete"
        detail="A comparison needs two image ids in its address. Pick two images to start over."
        action={{ label: "Choose images", href: "/" }}
      />
    );
  }

  // The grid is one row, explicitly `minmax(0,1fr)`: with the default `auto`
  // the row grows to the tallest column, both panels lose their internal
  // scroll, and the virtualized tree ends up measuring a viewport the size of
  // the whole list.
  return (
      <div className="grid h-full min-h-0 grid-cols-[400px_minmax(560px,1fr)] grid-rows-[minmax(0,1fr)] gap-6 px-8 py-6 max-[1279px]:grid-cols-[360px_minmax(0,1fr)] max-[1279px]:gap-6 max-[1279px]:px-6">
      <div className="flex min-h-0 flex-col">
        {query.isPending ? <LayerGraphSkeleton /> : null}
        {query.isError ? (
          <ErrorPanel
            title="This comparison could not be loaded"
            detail={
              query.error instanceof ApiError
                ? query.error.message
                : "The layer graph request failed."
            }
            action={{ label: "Choose images", href: "/" }}
          />
        ) : null}
        {graph === undefined || selection === null ? null : (
          <LayerGraphPanel graph={graph} selection={selection} onSelect={dispatch} />
        )}
      </div>
      <FsDiffPanel urlState={urlState} selection={selection} onUrlChange={updateUrl} />
    </div>
  );
}
