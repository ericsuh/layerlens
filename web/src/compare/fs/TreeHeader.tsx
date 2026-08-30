import { TREE_COLUMNS } from "./columns";

/**
 * The sticky column header (DESIGN §5.3, RESEARCH Q12 fix 1).
 *
 * It applies `.ll-tgrid` — the *same* class the rows apply — so alignment is a
 * property of the stylesheet rather than of two numbers that have to be kept
 * equal. `aria-hidden`: `role=tree` has no column semantics, and every value
 * is already spelled out with its meaning in each row's SR sentence (§7), so
 * announcing the header would only add noise.
 */
export function TreeHeader() {
  return (
    <div className="ll-tgrid ll-thead" aria-hidden="true" data-testid="tree-header">
      {TREE_COLUMNS.map((column) => (
        <div
          key={column.key}
          title={column.title}
          data-testid={`col-${column.key}`}
          className={
            (column.key === "name"
              ? "ll-th-name"
              : column.key === "status"
                ? "ll-th-status"
                : "ll-th-num") + (column.hideBelow1280 === true ? " ll-tcol-optional" : "")
          }
        >
          {column.label}
        </div>
      ))}
    </div>
  );
}
