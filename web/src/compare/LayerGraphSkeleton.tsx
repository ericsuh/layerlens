/**
 * DESIGN state #18: the layer section while the graph is still being
 * assembled. The ghost matches the real anatomy — a trunk of three, a fork,
 * two branch cards a side — so nothing moves when the data lands.
 */
export function LayerGraphSkeleton() {
  return (
    <section className="flex min-h-0 flex-col" aria-busy="true" aria-label="Loading layer comparison">
      <header className="mb-4 flex flex-none items-baseline gap-2.5">
        <h2 className="text-section m-0">Layer comparison</h2>
        <span className="text-text-muted text-[12px]">reading the layer stacks…</span>
      </header>
      <div className="flex flex-col gap-3.5">
        {[0, 1, 2].map((i) => (
          <div
            key={i}
            className="border-border bg-surface flex flex-col gap-2.5 rounded-[10px] border p-3"
          >
            <div className="ll-skeleton h-3.5 w-3/5" />
            <div className="ll-skeleton h-2.5 w-2/5" />
          </div>
        ))}
      </div>
      <div className="mt-5 grid grid-cols-2 gap-5">
        {[0, 1].map((column) => (
          <div key={column} className="flex flex-col gap-7">
            {[0, 1].map((i) => (
              <div
                key={i}
                className="border-border bg-surface flex flex-col gap-2.5 rounded-[10px] border p-3"
              >
                <div className="ll-skeleton h-3.5 w-4/5" />
                <div className="ll-skeleton h-2.5 w-1/2" />
              </div>
            ))}
          </div>
        ))}
      </div>
    </section>
  );
}
