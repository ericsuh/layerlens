import type { ReactNode } from "react";

export type SourceTabId = "analyzed" | "docker" | "registry";

interface TabDef {
  id: SourceTabId;
  label: string;
  /** Disabled tabs render at full size so adding their panels shifts nothing. */
  enabled: boolean;
  note: string;
}

/**
 * The source segmented control (DESIGN §4.3).
 *
 * All three tabs are laid out now, at their final size, and the two whose
 * sources are not implemented yet are genuinely `disabled` with a "soon" chip
 * — not clickable buttons that lead nowhere. Rendering them now is what keeps
 * the control from resizing when their panels land.
 */
const TABS: TabDef[] = [
  { id: "analyzed", label: "Analyzed", enabled: true, note: "" },
  { id: "docker", label: "Docker daemon", enabled: false, note: "not available yet" },
  { id: "registry", label: "Registry", enabled: false, note: "not available yet" },
];

export function SourceTabs({
  active,
  onChange,
  analyzedCount,
  children,
}: {
  active: SourceTabId;
  onChange: (id: SourceTabId) => void;
  analyzedCount: number | null;
  children: ReactNode;
}) {
  return (
    <div>
      <div className="ll-tabs mb-4" role="tablist" aria-label="Image sources">
        {TABS.map((tab) => (
          <button
            key={tab.id}
            type="button"
            role="tab"
            id={`source-tab-${tab.id}`}
            aria-selected={active === tab.id}
            aria-controls={`source-panel-${tab.id}`}
            aria-disabled={tab.enabled ? undefined : true}
            disabled={!tab.enabled}
            tabIndex={active === tab.id ? 0 : -1}
            title={tab.enabled ? undefined : `${tab.label} — ${tab.note}`}
            className="ll-tab"
            onClick={() => {
              onChange(tab.id);
            }}
          >
            {tab.id === "docker" ? <span className="ll-dot" aria-hidden="true" /> : null}
            <span>{tab.label}</span>
            {tab.id === "analyzed" && analyzedCount !== null ? (
              <span className="ll-count-chip ll-num">{analyzedCount}</span>
            ) : null}
            {tab.enabled ? null : <span className="ll-count-chip">soon</span>}
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
