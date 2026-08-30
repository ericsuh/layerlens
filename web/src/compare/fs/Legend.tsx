import { HatchSwatch } from "./SizeBar";

const ENTRIES = [
  { kind: "added", glyph: "+", label: "added", className: "text-added" },
  { kind: "removed", glyph: "−", label: "removed", className: "text-removed" },
  { kind: "modified", glyph: "±", label: "modified", className: "text-modified" },
  { kind: "unchanged", glyph: "·", label: "unchanged", className: "text-unchanged" },
] as const;

/**
 * The diff legend (DESIGN §5.3). Each entry pairs the glyph with the *hatch*
 * the size bars use, because that pairing is what makes the tree readable
 * without colour — the accessibility rule and the spec's "coloured and
 * hatched" requirement are the same requirement.
 */
export function Legend() {
  return (
    <ul className="text-text-muted m-0 flex flex-none list-none gap-2.5 p-0 text-[11.5px]">
      {ENTRIES.map((entry) => (
        <li key={entry.kind} title={`Entries ${entry.label} in image B relative to image A`}>
          <HatchSwatch kind={entry.kind} />
          <span className={entry.className} aria-hidden="true">
            {entry.glyph}
          </span>{" "}
          {entry.label}
        </li>
      ))}
    </ul>
  );
}
