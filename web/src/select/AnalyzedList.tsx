import { useId, useMemo, useState } from "react";

import type { ImageSummary } from "../api/types";
import { DigestValue } from "../components/DigestValue";
import { ImageBadge, ImageRefText, imageSummaryLine, primaryRef } from "../components/identity";
import { EmptyPanel } from "../components/states";
import { formatRelativeTime } from "../lib/format";
import { slotOf } from "./slots";
import type { SlotState } from "./slots";
import type { Side } from "../components/identity";

/** Lists over this many rows get a filter input (DESIGN §4.3). */
const FILTER_THRESHOLD = 8;

/**
 * The demo pair DESIGN §4.3 names by reference, pinned first with a "demo"
 * chip. Deliberately not derived from the API's `source: "fixture"`: every
 * bundled edge-case image is a fixture, but only these two are the walkthrough
 * the product leads with.
 */
const DEMO_REFS = new Set(["example:v1", "example:v2"]);

function isDemo(image: ImageSummary): boolean {
  return image.refNames.some((ref) => DEMO_REFS.has(ref));
}

export function sortImages(images: ImageSummary[]): ImageSummary[] {
  return [...images].sort((a, b) => {
    const demoDelta = Number(isDemo(b)) - Number(isDemo(a));
    if (demoDelta !== 0) {
      return demoDelta;
    }
    return primaryRef(a).localeCompare(primaryRef(b));
  });
}

export function filterImages(images: ImageSummary[], query: string): ImageSummary[] {
  const needle = query.trim().toLowerCase();
  if (needle === "") {
    return images;
  }
  return images.filter(
    (image) =>
      image.refNames.some((ref) => ref.toLowerCase().includes(needle)) ||
      image.id.toLowerCase().includes(needle),
  );
}

function SetButton({ side, onClick }: { side: Side; onClick: () => void }) {
  return (
    <button
      type="button"
      className={`ll-set-btn ll-set-btn-${side}`}
      onClick={(event) => {
        event.stopPropagation();
        onClick();
      }}
    >
      Set {side.toUpperCase()}
    </button>
  );
}

export function AnalyzedRow({
  image,
  slot,
  onPick,
  onSet,
  now,
}: {
  image: ImageSummary;
  slot: Side | null;
  onPick: () => void;
  onSet: (side: Side) => void;
  now?: Date;
}) {
  const ref = primaryRef(image);
  return (
    <div
      className={[
        "ll-src-row",
        slot === "a" ? "ll-src-row-a" : "",
        slot === "b" ? "ll-src-row-b" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid={`analyzed-row-${ref}`}
      role="button"
      tabIndex={0}
      aria-pressed={slot !== null}
      aria-label={
        slot === null
          ? `${ref}, ${imageSummaryLine(image)}. Select for image slot.`
          : `${ref}, ${imageSummaryLine(image)}. Currently image ${slot.toUpperCase()}. Select again to remove.`
      }
      onClick={onPick}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onPick();
        }
      }}
    >
      {slot === null ? null : <ImageBadge side={slot} />}
      <ImageRefText refName={ref} className="min-w-[180px] flex-1 font-mono text-[13px]" />
      {isDemo(image) ? <span className="ll-chip ll-chip-demo">demo</span> : null}
      <DigestValue digest={image.id} label="Image id" withPrefix={false} className="flex-none" />
      <span className="text-text-muted w-[150px] flex-none text-right text-[12px]">
        {imageSummaryLine(image)}
        <br />
        analyzed {formatRelativeTime(image.ingestedAt, now)}
      </span>
      <span className="ll-set-btns">
        <SetButton
          side="a"
          onClick={() => {
            onSet("a");
          }}
        />
        <SetButton
          side="b"
          onClick={() => {
            onSet("b");
          }}
        />
      </span>
    </div>
  );
}

export function AnalyzedListSkeleton() {
  return (
    <div data-testid="analyzed-skeleton" aria-busy="true" aria-label="Loading analyzed images">
      {[0, 1, 2, 3].map((i) => (
        <div key={i} className="border-border flex items-center gap-3 border-b px-4 py-3.5">
          <div className="ll-skeleton h-3.5 w-2/5" />
          <div className="ll-skeleton ml-auto h-3 w-24" />
        </div>
      ))}
    </div>
  );
}

export function AnalyzedList({
  images,
  slots,
  onPick,
  onSet,
  now,
}: {
  images: ImageSummary[];
  slots: SlotState;
  onPick: (id: string) => void;
  onSet: (side: Side, id: string) => void;
  now?: Date;
}) {
  const [filter, setFilter] = useState("");
  const filterId = useId();
  const sorted = useMemo(() => sortImages(images), [images]);
  const visible = useMemo(() => filterImages(sorted, filter), [sorted, filter]);

  if (images.length === 0) {
    return (
      <EmptyPanel
        title="No analyzed images yet"
        detail="Nothing has been analyzed on this server. The Docker daemon and registry sources will let you add one."
      />
    );
  }

  return (
    <div>
      {images.length > FILTER_THRESHOLD ? (
        <div className="border-border flex items-center gap-2 border-b px-4 py-2.5">
          <label htmlFor={filterId} className="text-label text-text-muted uppercase">
            Filter
          </label>
          <input
            id={filterId}
            type="search"
            value={filter}
            placeholder="Filter by reference…"
            className="border-border-strong bg-surface text-text w-[240px] rounded-md border px-2.5 py-1.5 font-mono text-[12.5px]"
            onChange={(event) => {
              setFilter(event.target.value);
            }}
          />
          <span className="text-text-muted ml-auto text-[11.5px]">
            showing {visible.length} of {images.length}
          </span>
        </div>
      ) : null}

      {visible.length === 0 ? (
        <EmptyPanel
          title={`No images match “${filter.trim()}”`}
          detail="Clear the filter to see every analyzed image on this server."
        />
      ) : (
        visible.map((image) => (
          <AnalyzedRow
            key={image.id}
            image={image}
            slot={slotOf(slots, image.id)}
            onPick={() => {
              onPick(image.id);
            }}
            onSet={(side) => {
              onSet(side, image.id);
            }}
            {...(now === undefined ? {} : { now })}
          />
        ))
      )}
    </div>
  );
}
