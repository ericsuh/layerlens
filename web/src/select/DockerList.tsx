import { useId, useMemo, useState } from "react";

import type { DockerImageSummary, DockerListing } from "../api/types";
import { ImageBadge, ImageRefText } from "../components/identity";
import type { Side } from "../components/identity";
import { EmptyPanel } from "../components/states";
import { formatBytes } from "../lib/format";
import { slotOf } from "./slots";
import type { SlotState } from "./slots";

/** Same threshold as the Analyzed tab: lists over ~8 rows get a filter (§4.3). */
const FILTER_THRESHOLD = 8;

/**
 * The only platform layerlens analyzes (`ingest.PlatformString`).
 *
 * The daemon reports the variant it would *run*, which on an arm64 host is
 * arm64 even for an image that also holds linux/amd64 — and layerlens will
 * analyze the amd64 one. A row saying "linux/arm64" that then analyzes
 * successfully reads as a contradiction unless the list says which variant it
 * takes, so it does, once, under the rows.
 */
const ANALYZED_PLATFORM = "linux/amd64";

export function filterDockerImages(
  images: readonly DockerImageSummary[],
  query: string,
): DockerImageSummary[] {
  const needle = query.trim().toLowerCase();
  if (needle === "") {
    return [...images];
  }
  return images.filter((image) =>
    image.reference.toLowerCase().includes(needle),
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

/**
 * One daemon image.
 *
 * An already-analyzed image is a slot target exactly like an Analyzed-tab row
 * — same affordances, same keyboard handling — because it *is* one of those
 * images; the daemon is only how the user found it. An image with no analysis
 * yet cannot go in a slot at all (there is no image id to put there), so it
 * gets a single explicit `Analyze` action instead of a click target that would
 * silently mean something different from every other row on the page.
 */
function DockerRow({
  image,
  slot,
  analyzing,
  onPick,
  onSet,
  onAnalyze,
}: {
  image: DockerImageSummary;
  slot: Side | null;
  analyzing: boolean;
  onPick: () => void;
  onSet: (side: Side) => void;
  onAnalyze: () => void;
}) {
  const meta = (
    <>
      {image.platform === undefined || image.platform === "" ? null : (
        <span className="text-text-muted w-[110px] flex-none text-right text-[12px]">
          {image.platform}
        </span>
      )}
      <span className="ll-num text-text-muted w-[90px] flex-none text-right text-[12px]">
        {formatBytes(image.sizeBytes)}
      </span>
    </>
  );

  if (image.alreadyAnalyzed && image.analyzedId !== undefined) {
    return (
      <div
        className={[
          "ll-src-row",
          slot === "a" ? "ll-src-row-a" : "",
          slot === "b" ? "ll-src-row-b" : "",
        ]
          .filter(Boolean)
          .join(" ")}
        data-testid={`docker-row-${image.reference}`}
        role="button"
        tabIndex={0}
        aria-pressed={slot !== null}
        aria-label={
          slot === null
            ? `${image.reference}, ${formatBytes(image.sizeBytes)}, analyzed. Select for image slot.`
            : `${image.reference}, ${formatBytes(image.sizeBytes)}. Currently image ${slot.toUpperCase()}. Select again to remove.`
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
        <ImageRefText
          refName={image.reference}
          className="min-w-[180px] flex-1 font-mono text-[13px]"
        />
        {meta}
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

  return (
    <div
      className="ll-src-row ll-src-row-static"
      data-testid={`docker-row-${image.reference}`}
    >
      <ImageRefText
        refName={image.reference}
        className="min-w-[180px] flex-1 font-mono text-[13px]"
      />
      <span className="text-text-muted text-[12px]">will be analyzed</span>
      {meta}
      <button
        type="button"
        className="ll-btn-ghost flex-none"
        disabled={analyzing}
        onClick={onAnalyze}
      >
        {analyzing ? "Analyzing…" : "Analyze"}
      </button>
    </div>
  );
}

/**
 * The Docker daemon source panel (DESIGN §4.3 tab 2, §9 rows 4–5).
 *
 * An unavailable daemon is not an error state: it renders the server's own
 * explanation and **no action button**, because there is nothing the user can
 * do about it from inside the app (state #4). The Retry case is state #6, a
 * daemon that answered and then failed, and it is a query error rather than a
 * listing — so it is handled by the caller, not here.
 */
export function DockerList({
  listing,
  slots,
  analyzing,
  onPick,
  onSet,
  onAnalyze,
}: {
  listing: DockerListing;
  slots: SlotState;
  /** References with a pull already in flight, so the row cannot double-submit. */
  analyzing: ReadonlySet<string>;
  onPick: (id: string) => void;
  onSet: (side: Side, id: string) => void;
  onAnalyze: (reference: string) => void;
}) {
  const [filter, setFilter] = useState("");
  const filterId = useId();
  const images = useMemo(() => listing.images ?? [], [listing.images]);
  const visible = useMemo(
    () => filterDockerImages(images, filter),
    [images, filter],
  );

  if (!listing.available) {
    return (
      <EmptyPanel
        title="The Docker daemon is unavailable"
        detail={
          listing.reason ??
          "No Docker daemon is reachable from this server, so the daemon source is unavailable."
        }
      />
    );
  }

  if (images.length === 0) {
    return (
      <EmptyPanel
        title="The Docker daemon has no images."
        detail="Build or pull an image locally and it will appear here."
      />
    );
  }

  return (
    <div>
      {images.length > FILTER_THRESHOLD ? (
        <div className="border-border flex items-center gap-2 border-b px-4 py-2.5">
          <label
            htmlFor={filterId}
            className="text-label text-text-muted uppercase"
          >
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
          detail="Clear the filter to see every image the daemon offers."
        />
      ) : (
        visible.map((image) => (
          <DockerRow
            key={image.dockerId + image.reference}
            image={image}
            slot={
              image.analyzedId === undefined
                ? null
                : slotOf(slots, image.analyzedId)
            }
            analyzing={analyzing.has(image.reference)}
            onPick={() => {
              if (image.analyzedId !== undefined) {
                onPick(image.analyzedId);
              }
            }}
            onSet={(side) => {
              if (image.analyzedId !== undefined) {
                onSet(side, image.analyzedId);
              }
            }}
            onAnalyze={() => {
              onAnalyze(image.reference);
            }}
          />
        ))
      )}

      {visible.some(
        (image) =>
          image.platform !== undefined &&
          image.platform !== "" &&
          image.platform !== ANALYZED_PLATFORM,
      ) ? (
        <p
          className="text-text-muted m-0 px-4 py-3 text-[12px]"
          data-testid="docker-platform-note"
        >
          The daemon names the variant it would run. layerlens analyzes the{" "}
          <span className="ll-num">{ANALYZED_PLATFORM}</span> variant of
          whichever image you pick.
        </p>
      ) : null}
    </div>
  );
}
