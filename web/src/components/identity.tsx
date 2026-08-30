import type { ImageSummary } from "../api/types";
import { formatBytes, shortDigest } from "../lib/format";
import { Tooltip } from "./ui/tooltip";

/** Which of the two comparison slots something belongs to. */
export type Side = "a" | "b";

export const SIDE_LABEL: Record<Side, string> = { a: "A", b: "B" };

export function ImageBadge({ side, className = "" }: { side: Side; className?: string }) {
  return (
    <span
      className={`ll-badge ll-badge-${side} ${className}`.trim()}
      aria-hidden="true"
      data-testid={`badge-${side}`}
    >
      {SIDE_LABEL[side]}
    </span>
  );
}

/**
 * An image reference, truncate-start (DESIGN §3): `…org/very-long-name:v2`.
 * The tail is what identifies the image, so the registry/namespace head is
 * what elides.
 */
export function ImageRefText({ refName, className = "" }: { refName: string; className?: string }) {
  return (
    <span className={`ll-ref-truncate ${className}`.trim()} title={refName}>
      <bdi>{refName}</bdi>
    </span>
  );
}

/** The display name of an image: its first ref, or its short id if untagged. */
export function primaryRef(image: ImageSummary): string {
  return image.refNames[0] ?? shortDigest(image.id);
}

/** `459 MiB · 8 layers` — the one-line summary used in slots and rows. */
export function imageSummaryLine(image: ImageSummary): string {
  return `${formatBytes(image.totalBytes)} · ${image.layerCount} layer${
    image.layerCount === 1 ? "" : "s"
  }`;
}

/**
 * The header's A/B identity chip: badge + truncate-start ref, with the full
 * ref and digest in a tooltip. Present identically in the header, the slots
 * and the diagram so "which one is A" never has to be re-derived (DESIGN §1.5).
 */
export function ImageChip({ side, image }: { side: Side; image: ImageSummary }) {
  const ref = primaryRef(image);
  return (
    <Tooltip
      content={
        <div className="flex flex-col gap-1">
          <span className="font-mono text-[12px]">{ref}</span>
          <span className="font-mono text-[12px] text-text-muted">{image.id}</span>
          <span className="text-text-muted">{imageSummaryLine(image)}</span>
        </div>
      }
      side="bottom"
    >
      {/* Focusable so the full ref and digest are reachable by keyboard too,
          not just on hover. It is data, not a control: no hover chrome, no
          pointer cursor (DESIGN §1.2). */}
      <span
        className="ll-img-chip"
        data-testid={`image-chip-${side}`}
        tabIndex={0}
        role="note"
        aria-label={`Image ${SIDE_LABEL[side]}: ${ref}`}
      >
        <ImageBadge side={side} />
        <ImageRefText refName={ref} />
      </span>
    </Tooltip>
  );
}
