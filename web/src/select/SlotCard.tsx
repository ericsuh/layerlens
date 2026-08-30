import type { ImageSummary } from "../api/types";
import { ImageBadge, ImageRefText, imageSummaryLine, primaryRef } from "../components/identity";
import { shortDigest } from "../lib/format";
import type { Side } from "../components/identity";

/**
 * One of the two A/B slots (DESIGN §4.2). The card is itself the "arm this
 * slot" control, so it carries all three interactive cues: a real border, a
 * hover background shift, and a pointer cursor.
 */
export function SlotCard({
  side,
  image,
  armed,
  onArm,
  onClear,
}: {
  side: Side;
  image: ImageSummary | null;
  armed: boolean;
  onArm: () => void;
  onClear: () => void;
}) {
  const filled = image !== null;
  return (
    <div
      className={[
        "ll-slot",
        `ll-slot-${side}`,
        filled ? "ll-slot-filled" : "ll-slot-empty",
        armed ? "ll-slot-armed" : "",
      ]
        .filter(Boolean)
        .join(" ")}
      data-testid={`slot-${side}`}
      data-armed={armed ? "true" : "false"}
      role="button"
      tabIndex={0}
      aria-pressed={armed}
      aria-label={
        filled
          ? `Image ${side.toUpperCase()} slot: ${primaryRef(image)}. Activate to make this the slot the next pick fills.`
          : `Image ${side.toUpperCase()} slot, empty. Activate to make this the slot the next pick fills.`
      }
      onClick={onArm}
      onKeyDown={(event) => {
        if (event.key === "Enter" || event.key === " ") {
          event.preventDefault();
          onArm();
        }
      }}
    >
      <ImageBadge side={side} />
      {filled ? (
        <>
          <span className="min-w-0 flex-1">
            <ImageRefText refName={primaryRef(image)} className="block font-mono text-[13px]" />
            <span className="text-text-muted mt-0.5 block truncate text-[12px]">
              {imageSummaryLine(image)} · <span className="font-mono">{shortDigest(image.id)}</span>
            </span>
          </span>
          <button
            type="button"
            className="ll-icon-btn text-text-muted h-6 w-6 flex-none border-0 text-[14px]"
            aria-label={`Clear image ${side.toUpperCase()}`}
            title={`Clear image ${side.toUpperCase()}`}
            onClick={(event) => {
              event.stopPropagation();
              onClear();
            }}
          >
            <span aria-hidden="true">✕</span>
          </button>
        </>
      ) : (
        <span className="text-text-muted flex-1">Select an image below</span>
      )}
    </div>
  );
}
