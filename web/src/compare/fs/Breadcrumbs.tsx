import { useState } from "react";

import { Popover } from "../../components/ui/popover";
import { breadcrumbTrail } from "./treeAdapter";

/**
 * The drill-down path (DESIGN §3, §5.3).
 *
 * Crumbs are buttons, not links: re-rooting is a change to the current view's
 * `path` param and stays inside the same comparison. Middle crumbs collapse
 * behind a `…` button that opens the full list, so a deep path never widens
 * the toolbar and the hidden segments stay reachable.
 */
export function Breadcrumbs({
  path,
  onNavigate,
}: {
  path: string;
  onNavigate: (path: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const trail = breadcrumbTrail(path);
  const last = trail.crumbs.length - 1;

  return (
    <nav className="flex min-w-0 flex-wrap items-center gap-0.5" aria-label="Directory path">
      {trail.crumbs.map((crumb, index) => (
        <span key={crumb.path} className="flex items-center gap-0.5">
          {index > 0 ? (
            <span className="text-text-muted text-[11px]" aria-hidden="true">
              ›
            </span>
          ) : null}
          {index === 1 && trail.hidden.length > 0 ? (
            <>
              <button
                type="button"
                className="ll-crumb"
                onClick={() => {
                  onNavigate(crumb.path);
                }}
              >
                {crumb.label}
              </button>
              <span className="text-text-muted text-[11px]" aria-hidden="true">
                ›
              </span>
              <Popover
                open={open}
                onOpenChange={setOpen}
                side="bottom"
                label="Hidden path segments"
                trigger={
                  <button
                    type="button"
                    className="ll-crumb"
                    data-testid="crumb-overflow"
                    aria-label={`Show ${String(trail.hidden.length)} hidden path segments`}
                  >
                    …
                  </button>
                }
              >
                <ul className="m-0 flex list-none flex-col gap-0.5 p-0">
                  {trail.hidden.map((hidden) => (
                    <li key={hidden.path}>
                      <button
                        type="button"
                        className="ll-crumb block w-full"
                        onClick={() => {
                          setOpen(false);
                          onNavigate(hidden.path);
                        }}
                      >
                        {hidden.label}
                      </button>
                    </li>
                  ))}
                </ul>
              </Popover>
            </>
          ) : index === last ? (
            <span className="ll-crumb" aria-current="page" data-testid="crumb-current">
              {crumb.label}
            </span>
          ) : (
            <button
              type="button"
              className="ll-crumb"
              onClick={() => {
                onNavigate(crumb.path);
              }}
            >
              {crumb.label}
            </button>
          )}
        </span>
      ))}
    </nav>
  );
}
