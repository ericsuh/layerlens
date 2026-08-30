import { useMemo, useReducer, useState } from "react";
import { useLocation } from "wouter";

import { ApiError } from "../api/client";
import { useImagesQuery } from "../api/queries";
import { ErrorPanel } from "../components/states";
import { SIDE_LABEL } from "../components/identity";
import type { Side } from "../components/identity";
import { compareHref, DEFAULT_FILTER, DEFAULT_PATH } from "../lib/urlstate";
import { AnalyzedList, AnalyzedListSkeleton } from "./AnalyzedList";
import { SlotCard } from "./SlotCard";
import { SourceTabs } from "./SourceTabs";
import type { SourceTabId } from "./SourceTabs";
import { bothFilled, initialSlotState, sameImage, slotReducer } from "./slots";

/**
 * The image-selection view (DESIGN §4). Slot state is local and ephemeral —
 * only a completed pair becomes URL state, on the way to /compare.
 */
export function SelectPage() {
  const [slots, dispatch] = useReducer(slotReducer, initialSlotState);
  const [tab, setTab] = useState<SourceTabId>("analyzed");
  const [, navigate] = useLocation();
  const query = useImagesQuery();

  const images = useMemo(() => query.data?.images ?? [], [query.data]);
  const byId = useMemo(() => new Map(images.map((image) => [image.id, image])), [images]);

  const ready = bothFilled(slots);
  const duplicate = sameImage(slots);

  const compare = () => {
    if (!ready) {
      return;
    }
    navigate(
      compareHref({
        left: slots.a,
        right: slots.b,
        l: null,
        r: null,
        path: DEFAULT_PATH,
        filter: DEFAULT_FILTER,
      }),
    );
  };

  return (
    <div className="mx-auto max-w-[960px] p-8">
      <h1 className="text-page-title m-0 mb-1">Compare two images</h1>
      <p className="text-text-muted m-0 mb-6">
        Pick an image for each slot, then compare their layers and filesystems.
      </p>

      <div className="grid grid-cols-2 gap-4 max-[719px]:grid-cols-1">
        {(["a", "b"] as const).map((side: Side) => (
          <SlotCard
            key={side}
            side={side}
            image={slots[side] === null ? null : (byId.get(slots[side]) ?? null)}
            armed={slots.armed === side}
            onArm={() => {
              dispatch({ type: "arm", side });
            }}
            onClear={() => {
              dispatch({ type: "clear", side });
            }}
          />
        ))}
      </div>

      {/* DESIGN §7: arming is announced, because it changes where the next
          plain click lands and that is not otherwise spoken. */}
      <p aria-live="polite" className="sr-only">
        {`Image ${SIDE_LABEL[slots.armed]} slot active`}
      </p>

      <div className="my-6 mb-8 flex items-center justify-center gap-3">
        <button
          type="button"
          className="ll-btn-primary"
          disabled={!ready}
          onClick={compare}
          data-testid="compare-button"
        >
          Compare layers →
        </button>
        <span className="text-text-muted text-[12px]" data-testid="compare-hint">
          {ready
            ? duplicate
              ? "Both slots contain the same image — every layer will be shared."
              : ""
            : "Choose two images to compare"}
        </span>
      </div>

      <SourceTabs
        active={tab}
        onChange={setTab}
        analyzedCount={query.isSuccess ? images.length : null}
      >
        {query.isPending ? <AnalyzedListSkeleton /> : null}
        {query.isError ? (
          <ErrorPanel
            title="Analyzed images could not be loaded"
            detail={
              query.error instanceof ApiError
                ? query.error.message
                : "The image list request failed."
            }
          />
        ) : null}
        {query.isSuccess ? (
          <AnalyzedList
            images={images}
            slots={slots}
            onPick={(id) => {
              dispatch({ type: "pick", id });
            }}
            onSet={(side, id) => {
              dispatch({ type: "set", side, id });
            }}
          />
        ) : null}
      </SourceTabs>
    </div>
  );
}
