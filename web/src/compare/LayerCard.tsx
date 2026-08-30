import type { KeyboardEvent, MouseEvent } from "react";

import type { GraphLayer } from "../api/types";
import { DigestValue } from "../components/DigestValue";
import { Popover } from "../components/ui/popover";
import { formatBytes, formatCount } from "../lib/format";
import { displayInstruction, instructionLabel } from "../lib/instruction";

export type CardColumn = "trunk" | "a" | "b";

const BAR_CLASS: Record<CardColumn, string> = {
  trunk: "ll-lc-bar-shared",
  a: "ll-lc-bar-a",
  b: "ll-lc-bar-b",
};

const CARD_CLASS: Record<CardColumn, string> = {
  trunk: "ll-lcard-shared",
  a: "ll-lcard-a",
  b: "ll-lcard-b",
};

/**
 * The copy for a could-be-shared pair. It is the honesty rule of the whole
 * feature (RESEARCH Q9): these layers were *not* shared — Docker built both,
 * because their ChainIDs differ — they merely turned out to contain the same
 * files. Nothing here may read as a cache hit, and the word "shared" never
 * appears unqualified.
 */
export const COULD_BE_SHARED_TITLE = "These layers could have been shared";
export const COULD_BE_SHARED_BODY =
  "Both layers produce identical files — same content and permissions, timestamps ignored — so they are equivalent under Docker's build-cache rule. They were still built and stored separately: an earlier layer differs between the two images, so the build cache had already diverged before this step.";
export const COULD_BE_SHARED_IDENTICAL =
  "Their layer tarballs are byte-identical (same DiffID), so a registry would deduplicate them even though the build did not.";
export const COULD_BE_SHARED_EQUIVALENT =
  "Their tarballs differ byte-for-byte (different DiffIDs) but their contents match once ordering and timestamps are normalized.";

export function CouldBeSharedExplanation({ diffIdEqual }: { diffIdEqual: boolean }) {
  return (
    <div className="flex flex-col gap-2">
      <p className="text-section m-0">{COULD_BE_SHARED_TITLE}</p>
      <p className="text-text-muted m-0">{COULD_BE_SHARED_BODY}</p>
      <p className="text-text-muted m-0">
        {diffIdEqual ? COULD_BE_SHARED_IDENTICAL : COULD_BE_SHARED_EQUIVALENT}
      </p>
    </div>
  );
}

export interface LayerCardProps {
  layer: GraphLayer;
  column: CardColumn;
  /** Layer count this card stands for: index + 1 (see selection.ts). */
  count: number;
  selected: boolean;
  /** Roving tabindex: exactly one card per radiogroup is tabbable. */
  tabbable: boolean;
  /** Denominator of the relative size bar: the largest layer in either image. */
  maxLayerBytes: number;
  /** Set when this layer has a could-be-shared partner in the other image. */
  edge?: { diffIdEqual: boolean; highlighted: boolean };
  onSelect: () => void;
  onKeyDown: (event: KeyboardEvent<HTMLDivElement>) => void;
  onHoverEdge?: (hovered: boolean) => void;
  registerRef: (element: HTMLDivElement | null) => void;
  /** id of the description explaining that a trunk card sets both points. */
  describedBy?: string;
}

export function LayerCard({
  layer,
  column,
  count,
  selected,
  tabbable,
  maxLayerBytes,
  edge,
  onSelect,
  onKeyDown,
  onHoverEdge,
  registerRef,
  describedBy,
}: LayerCardProps) {
  const display = displayInstruction(layer);
  const empty = layer.contentBytes === 0;
  // Layers under 1% of the largest still get a visible sliver rather than
  // vanishing (DESIGN §5.2); 0 B layers get no bar at all.
  const percent = empty
    ? 0
    : Math.max(2, Math.round((layer.contentBytes / Math.max(maxLayerBytes, 1)) * 100));

  const sizeText = empty ? "0 B · empty" : formatBytes(layer.contentBytes);
  const ariaLabel = [
    column === "trunk" ? "Shared layer" : column === "a" ? "Image A layer" : "Image B layer",
    String(count),
    instructionLabel(display),
    sizeText,
    `${formatCount(layer.entryCount)} entries`,
    edge ? "could have been shared with a layer in the other image" : "",
  ]
    .filter((part) => part !== "")
    .join(", ");

  const stop = (event: MouseEvent) => {
    event.stopPropagation();
  };

  return (
    <div
      ref={registerRef}
      role="radio"
      aria-checked={selected}
      aria-label={ariaLabel}
      {...(describedBy === undefined ? {} : { "aria-describedby": describedBy })}
      tabIndex={tabbable ? 0 : -1}
      data-testid={`layer-card-${column}-${count}`}
      data-selected={selected ? "true" : "false"}
      className={[
        "ll-lcard",
        CARD_CLASS[column],
        selected ? "ll-lcard-selected" : "",
        edge?.highlighted ? "ll-lcard-paired" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      onClick={onSelect}
      onKeyDown={onKeyDown}
      onMouseEnter={onHoverEdge ? () => onHoverEdge(true) : undefined}
      onMouseLeave={onHoverEdge ? () => onHoverEdge(false) : undefined}
    >
      <span className="ll-radio" aria-hidden="true" />

      <span className="min-w-0 flex-1">
        <span className="flex min-w-0 items-baseline gap-1.5">
          {display.keyword === "" ? null : (
            <span className="flex-none font-semibold">{display.keyword}</span>
          )}
          <span
            className={`ll-lc-instruction ${display.unknown ? "ll-lc-unknown" : ""}`.trim()}
            title={display.unknown ? "No Dockerfile instruction could be mapped to this layer" : display.raw}
          >
            {display.rest}
          </span>
          {display.unknown ? null : (
            <Popover
              label="Raw Dockerfile instruction"
              trigger={
                <button
                  type="button"
                  className="ll-btn-ghost ll-raw-btn flex-none px-1.5 py-0 text-[11px] leading-4"
                  onClick={stop}
                  aria-label="Show the raw instruction"
                  title="Show the raw instruction"
                >
                  <span aria-hidden="true">⋯</span>
                </button>
              }
            >
              <div className="flex flex-col gap-2">
                <span className="text-label text-text-muted uppercase">Raw instruction</span>
                <div className="ll-overlay-scroll">{display.raw}</div>
              </div>
            </Popover>
          )}
        </span>

        <span className="text-text-muted mt-1.5 flex items-center gap-2 text-[12px]">
          <span className="ll-mono ll-num flex-none" style={{ minWidth: "58px" }}>
            {sizeText}
          </span>
          {empty ? null : (
            <span className={`ll-lc-bar ${BAR_CLASS[column]}`} aria-hidden="true">
              <i style={{ width: `${percent}%` }} />
            </span>
          )}
          {column === "trunk" ? (
            <span className="ml-auto flex-none">
              <DigestValue
                digest={layer.diffId}
                label="DiffID"
                withPrefix={false}
                {...(layer.compressedDigest === undefined
                  ? {}
                  : { secondary: { label: "Compressed digest", value: layer.compressedDigest } })}
              />
            </span>
          ) : null}
        </span>
      </span>

      {edge ? (
        <Popover
          label="Could-be-shared layers"
          trigger={
            <button
              type="button"
              className="ll-approx-chip"
              onClick={stop}
              onMouseEnter={onHoverEdge ? () => onHoverEdge(true) : undefined}
              onMouseLeave={onHoverEdge ? () => onHoverEdge(false) : undefined}
              aria-label="Could have been shared with a layer in the other image — explain"
            >
              <span aria-hidden="true">≈</span>
            </button>
          }
        >
          <CouldBeSharedExplanation diffIdEqual={edge.diffIdEqual} />
        </Popover>
      ) : null}

      {column === "trunk" ? <span className="ll-shared-tag">SHARED</span> : null}
    </div>
  );
}
