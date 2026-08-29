import { formatBytes } from "./lib/format";

const SWATCHES = [
  { label: "shared", className: "bg-shared-tint text-shared" },
  { label: "added", className: "bg-added-tint text-added-strong" },
  { label: "removed", className: "bg-removed-tint text-removed-strong" },
  { label: "modified", className: "bg-modified-tint text-modified-strong" },
  { label: "image A", className: "bg-image-a-tint text-image-a" },
  { label: "image B", className: "bg-image-b-tint text-image-b" },
] as const;

/**
 * Placeholder shell for the phase-001 walking skeleton: it proves the bundled
 * React actually executes in the browser and that the Tailwind theme tokens
 * reach the page. The real views land in phases 006–007.
 */
export function App() {
  return (
    <main className="mx-auto flex max-w-3xl flex-col gap-6 p-8">
      <header className="flex flex-col gap-2">
        <h1 className="text-page-title" data-testid="app-title">
          layerlens
        </h1>
        <p className="text-body text-text-muted">
          Compare container image layers and the filesystems they build up.
        </p>
      </header>

      <section className="rounded-panel border border-border bg-surface p-4 shadow-panel">
        <h2 className="text-section">Toolchain check</h2>
        <p className="text-body text-text-muted mt-2">
          Rendered by React from the embedded bundle. Example humanized size:{" "}
          <span className="font-mono" data-testid="sample-size">
            {formatBytes(15_000_000)}
          </span>
          .
        </p>
        <ul className="mt-4 flex flex-wrap gap-2">
          {SWATCHES.map((swatch) => (
            <li
              key={swatch.label}
              className={`rounded-badge px-3 py-1 text-label uppercase ${swatch.className}`}
            >
              {swatch.label}
            </li>
          ))}
        </ul>
      </section>
    </main>
  );
}
