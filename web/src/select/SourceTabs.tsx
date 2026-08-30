import { useRef } from "react";
import type { ReactNode } from "react";

export type SourceTabId = "analyzed" | "docker" | "registry";

interface TabDef {
  id: SourceTabId;
  label: string;
}

/**
 * The source segmented control (DESIGN §4.3).
 *
 * All three sources are live. The strip's geometry is fixed by the three
 * labels — the chips are the only variable part, and the panel below keeps its
 * 320px floor — so switching tabs never moves anything.
 */
const TABS: readonly TabDef[] = [
  { id: "analyzed", label: "Analyzed" },
  { id: "docker", label: "Docker daemon" },
  { id: "registry", label: "Registry" },
];

export function SourceTabs({
  active,
  onChange,
  analyzedCount,
  dockerAvailable,
  children,
}: {
  active: SourceTabId;
  onChange: (id: SourceTabId) => void;
  analyzedCount: number | null;
  /** null while the daemon listing is still in flight — neither dot state yet. */
  dockerAvailable: boolean | null;
  children: ReactNode;
}) {
  const strip = useRef<HTMLDivElement | null>(null);

  /**
   * A tablist is one tab stop: Tab reaches the selected tab and the arrow keys
   * move between them (WAI-ARIA), which is why every other tab carries
   * `tabIndex={-1}`. Without this handler those tabs would be unreachable by
   * keyboard altogether.
   */
  const moveFocus = (event: React.KeyboardEvent, index: number) => {
    const destination: Record<string, number | undefined> = {
      ArrowRight: (index + 1) % TABS.length,
      ArrowLeft: (index - 1 + TABS.length) % TABS.length,
      Home: 0,
      End: TABS.length - 1,
    };
    const next = destination[event.key];
    const target = next === undefined ? undefined : TABS[next];
    if (target === undefined) {
      return;
    }
    event.preventDefault();
    onChange(target.id);
    strip.current?.querySelector<HTMLButtonElement>(`#source-tab-${target.id}`)?.focus();
  };

  return (
    <div>
      <div className="ll-tabs mb-4" role="tablist" aria-label="Image sources" ref={strip}>
        {TABS.map((tab, index) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`source-tab-${tab.id}`}
            aria-selected={active === tab.id}
            aria-controls={`source-panel-${tab.id}`}
            tabIndex={active === tab.id ? 0 : -1}
            className="ll-tab"
            onClick={() => {
              onChange(tab.id);
            }}
            onKeyDown={(event) => {
              moveFocus(event, index);
            }}
          >
            {tab.id === "docker" ? (
              <span
                className={`ll-dot ${dockerAvailable === true ? "ll-dot-ok" : ""}`.trim()}
                aria-hidden="true"
              />
            ) : null}
            <span>{tab.label}</span>
            {tab.id === "analyzed" && analyzedCount !== null ? (
              <span className="ll-count-chip ll-num">{analyzedCount}</span>
            ) : null}
            {/* Never the dot alone: the status is also a word (DESIGN §7). */}
            {tab.id === "docker" && dockerAvailable === false ? (
              <span className="ll-count-chip">unavailable</span>
            ) : null}
          </button>
        ))}
      </div>

      <div
        role="tabpanel"
        id={`source-panel-${active}`}
        aria-labelledby={`source-tab-${active}`}
        className="border-border bg-surface shadow-panel min-h-[320px] rounded-[10px] border"
      >
        {children}
      </div>
    </div>
  );
}
