import { selectionLabel } from "./selection";
import type { LayerSelection } from "./selection";

const SKELETON_ROWS = [86, 64, 72, 55, 68, 48, 60, 40];

/**
 * The filesystem-diff column while its data is still being assembled
 * (DESIGN state #18/#24): the panel chrome is real — the heading, the
 * comparison line and the legend all come from live selection state — and only
 * the tree body is a skeleton, so the layout never jumps when rows arrive.
 */
export function FsDiffPanel({ selection }: { selection: LayerSelection | null }) {
  return (
    <section className="flex min-h-0 flex-col" aria-labelledby="fs-section-title">
      <header className="mb-4 flex flex-none flex-wrap items-baseline gap-2.5">
        <h2 id="fs-section-title" className="text-section m-0">
          Filesystem diff
        </h2>
        <span className="text-text-muted text-[12px]">
          cumulative up to the selected layers · B relative to A
        </span>
      </header>

      <div
        className="border-border bg-surface shadow-panel flex min-h-0 flex-1 flex-col rounded-[10px] border"
        aria-busy="true"
        aria-describedby="fs-status"
      >
        <div className="border-border flex flex-col gap-2 border-b px-3.5 py-2.5">
          <div className="ll-mono text-text">/</div>
          <div className="flex flex-wrap items-center gap-2.5">
            {/* No comparison line until the layer counts are known: naming a
                layer we have not read yet would be a guess. */}
            <span className="text-text-muted text-[12px]">
              {selection === null ? (
                <span className="ll-skeleton inline-block h-3 w-48 align-middle" />
              ) : (
                <>
                  Comparing <b className="text-image-a">{selectionLabel("a", selection.l)}</b> vs{" "}
                  <b className="text-image-b">{selectionLabel("b", selection.r)}</b>
                </>
              )}
            </span>
            <span className="text-text-muted ml-auto flex flex-none gap-2.5 text-[11.5px]">
              <span>
                <span className="text-added" aria-hidden="true">
                  +
                </span>{" "}
                added
              </span>
              <span>
                <span className="text-removed" aria-hidden="true">
                  −
                </span>{" "}
                removed
              </span>
              <span>
                <span className="text-modified" aria-hidden="true">
                  ±
                </span>{" "}
                modified
              </span>
              <span>
                <span className="text-unchanged" aria-hidden="true">
                  ·
                </span>{" "}
                unchanged
              </span>
            </span>
          </div>
        </div>

        <div className="flex-1 overflow-hidden p-2" data-testid="fs-skeleton">
          {SKELETON_ROWS.map((width, index) => (
            <div key={width * 100 + index} className="flex h-8 items-center gap-3 px-2">
              <div className="ll-skeleton h-3" style={{ width: `${String(width)}%` }} />
              <div className="ll-skeleton ml-auto h-3 w-16 flex-none" />
            </div>
          ))}
        </div>

        <p id="fs-status" className="sr-only">
          Building the filesystem diff for the selected layers.
        </p>
      </div>
    </section>
  );
}
