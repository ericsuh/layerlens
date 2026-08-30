import { useEffect, useRef, useState } from "react";

import { formatCount } from "../../lib/format";
import type { TreeFilter } from "../../lib/urlstate";

const OPTIONS: { value: TreeFilter; label: string }[] = [
  { value: "changed", label: "Changed only" },
  { value: "all", label: "All entries" },
  { value: "added", label: "Added" },
  { value: "removed", label: "Removed" },
  { value: "modified", label: "Modified" },
];

/**
 * The filter menu and the name filter (DESIGN §5.3).
 *
 * "Changed only" is the default because unchanged entries are the
 * overwhelming majority and the diff is the point — and the control always
 * shows the active filter, so hidden data is never a mystery. `All entries`
 * and `Changed only` are the server's own filters; the three polarity
 * refinements narrow the same response client-side.
 */
export function FilterToggle({
  filter,
  onFilterChange,
  nameFilter,
  onNameFilterChange,
  shown,
  total,
}: {
  filter: TreeFilter;
  onFilterChange: (filter: TreeFilter) => void;
  nameFilter: string;
  onNameFilterChange: (value: string) => void;
  shown: number;
  total: number;
}) {
  // Debounced locally (DESIGN §5.3, 150 ms): every keystroke otherwise
  // re-walks and re-flattens the loaded tree. The draft is seeded once and
  // then owned here — this input is the only writer of the name filter, so
  // there is no upstream value to synchronize back from.
  const [draft, setDraft] = useState(nameFilter);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current !== null) {
        clearTimeout(timer.current);
      }
    },
    [],
  );

  return (
    <div className="flex flex-wrap items-center gap-2">
      <select
        className="ll-select"
        aria-label="Filter entries"
        data-testid="filter-select"
        value={filter}
        onChange={(event) => {
          onFilterChange(event.target.value as TreeFilter);
        }}
      >
        {OPTIONS.map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </select>
      <input
        className="ll-input"
        type="search"
        placeholder="Filter by name…"
        aria-label="Filter by name"
        data-testid="name-filter"
        value={draft}
        onChange={(event) => {
          const value = event.target.value;
          setDraft(value);
          if (timer.current !== null) {
            clearTimeout(timer.current);
          }
          timer.current = setTimeout(() => {
            onNameFilterChange(value);
          }, 150);
        }}
      />
      <span
        className="text-text-muted ml-auto flex-none text-[11.5px]"
        data-testid="showing-count"
      >
        showing {formatCount(shown)} of {formatCount(total)} entries
      </span>
    </div>
  );
}
