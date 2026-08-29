# Phase 007 — Frontend: filesystem diff tree + golden-workflow e2e

## Goal

Build the filesystem diff section — the virtualized unified tree with the
**corrected column spec** (DESIGN §5.3, RESEARCH Q12), hatched size bars,
changed-only filter, disclosure and drill-down — and wire Playwright so the
PROJECT.md golden workflow passes end-to-end against the real embedded-SPA
binary running on fixtures with no Docker and no network. This phase completes
the demo: everything in PROJECT.md's Acceptance Criteria is demonstrable when
it closes.

## Scope

**In:** `<FsDiffPanel>` per ARCHITECTURE §8.1/§8.4 (`Breadcrumbs`,
`FilterToggle`, `DiffTree` on `@headless-tree/react` + `@tanstack/react-virtual`,
`TreeRowView`); sticky toolbar (breadcrumbs, filter menu, name filter input,
legend); lazy per-directory `useInfiniteQuery` loading + `Show N more…`
pagination rows; drill-down via breadcrumb/`↳` re-rooting (`path` URL param);
selection-change dim-and-refresh treatment (DESIGN §6, state #24); all DESIGN
§9 tree states (#24–#29 + #25 empty diff, #26 filter-empty); row treatments,
glyphs, hatching per DESIGN §2.5/§5.3; a11y tree semantics + SR row text
(DESIGN §7); responsive column-hiding (DESIGN §8); Playwright config +
`mise run e2e` + the §9.4 non-network suites; remaining §9.3 TS unit tests.

**Not in this phase:** pull/docker UI and their e2e error paths (phase 008);
any server change beyond bug fixes discovered by e2e (fix + note delta if
contract-affecting).

## Prerequisites

Phase 006 (shell, compare page, URL state) — and transitively 005's API.

## Files to create/modify

- `web/src/compare/fs/FsDiffPanel.tsx`, `Breadcrumbs.tsx`, `FilterToggle.tsx`,
  `Legend.tsx`, `DiffTree.tsx`, `TreeRowView.tsx`, `SizeBar.tsx`
- `web/src/compare/fs/treeAdapter.ts` + tests (expanded-set × query pages →
  flat visible list for headless-tree/virtualizer)
- `web/src/compare/fs/columns.ts` — the single column-grid definition (widths
  from DESIGN §3) shared by header row and data rows
- `playwright.config.ts` (webServer per DECISIONS §D: build + run
  `./bin/layerlens --listen :43117 --data-dir .e2e-data --fixtures-dir fixtures`,
  wait on `/healthz`)
- `e2e/golden.spec.ts`, `e2e/degenerate.spec.ts`, `e2e/pagination.spec.ts`,
  `e2e/url-share.spec.ts`
- `mise.toml` — `[tasks.e2e] depends = ["build"] run = "playwright test"`.

## Implementation steps

1. `columns.ts`: one CSS-grid template used by header and rows —
   `Name (fluid) | ± 42px | Δ size 84px | Δ files 64px | Size 76px | Files 56px | Rel. size 108px`
   (DESIGN §5.3); indent guides/chevron live *inside* the Name cell so
   alignment holds at every depth by construction.
2. `DiffTree`: headless-tree over the flat adapter; TanStack Virtual for rows
   (fixed 32px); per-directory `useInfiniteQuery(['tree', left, right, l, r,
   path, filter])` with `getNextPageParam` (§8.2); watermark-triggered
   `fetchNextPage`; skeleton rows on expand; error row + Retry (state #27);
   `Show N more…` row (state #28); stale-cursor `bad_request` → reset to page 1.
3. `TreeRowView` per DESIGN §5.3 "Tree rows": disclosure triangle vs name-click
   toggle vs `↳` drill-down as three distinct affordances; status glyph column
   (`+ − ± ·`, dirs show `± N` compact descendant count); signed colored
   `Δ size`/`Δ files` (zero → muted `—`; files leave `Δ files`/`Files` empty);
   B-side absolutes with A-side tooltip (`A: … → B: …`, RESEARCH Q11); removed
   rows struck-through showing A-side values; segmented hatched size bar
   normalized by `maxSiblingBytes` (45° added / 135° removed / vertical-tick
   modified / solid unchanged; ≥2px clamp).
4. Toolbar: breadcrumbs with middle-crumb collapse (DESIGN §3) driving `path`;
   filter menu (All / Changed only default / Added / Removed / Modified — the
   server supports `all|changed`; Added/Removed/Modified refine client-side
   within loaded rows, menu chip always shows active filter) with
   "showing N of M" from `totalRows`; name-filter input filtering loaded rows
   client-side (see Risks re: DESIGN's server-assisted wish); legend with
   hatched swatches.
5. Selection-change treatment: keep old tree dimmed 60% + top progress bar
   until the new key's first page lands (state #24); reset disclosure set when
   pair/selection changes (§8.3).
6. A11y: `role=tree`/`treeitem` with `aria-level/setsize/posinset/expanded`
   from server `childCount`; composed SR row text per DESIGN §7; header row
   `aria-hidden`; bars `aria-hidden`.
7. Playwright suites (below); run headed locally on the Mac, headless in the
   task; no CI wiring (PROJECT.md: local Mac only, no CI/DinD).
8. Gates; update IMPLEMENTATION.md status; commit.

## Test cases

Vitest (`treeAdapter.test.ts`, `SizeBar.test.tsx`, component tests):
- `adapter_flatten_respects_expansion_and_order` (dirs first, name asc, pages
  appended in order).
- `adapter_page_append_no_duplicates`; `adapter_stale_cursor_resets_to_page1`.
- `sizebar_math`: width from agg vs `maxSiblingBytes`; zero-denominator guard;
  <2px clamp; segment split adds up to total width.
- `row_rendering_by_status` (Testing Library): added/removed/modified/
  unchanged/dir-containing-changes each get the DESIGN treatment (tint, glyph,
  strike-through, `± N` summary; no tint on merely-containing dirs).
- `delta_formatting_in_cells`: `−1023.9 MiB` worst case fits (assert
  `white-space: nowrap` + fixed track, no wrap in jsdom via class contract).
- `breadcrumb_collapse_middle`.

Playwright (§9.4, fixtures only):
- `golden.spec.ts` — **the acceptance criterion, asserted step by step**: open
  app → pick `example:v1` + `example:v2` → layer view shows shared trunk +
  fork + attributed branches with instructions → dotted edge visible between
  npm layers → select one layer per branch → tree shows `debug.log` added,
  `main.js` modified, apt whiteout paths removed → folder rows show aggregate
  sizes (human units), counts, deltas, bars → disclosure expands in place →
  drill-down re-roots with breadcrumbs → copy URL into new context → identical
  view.
- `degenerate.spec.ts`: strict-prefix pair (empty branch renders, selection
  works); disjoint pair (trunkLength 0 strip); trunk-point self-diff
  (all-unchanged + `filter=changed` empty state #25/#26 with "Show all").
- `pagination.spec.ts`: `wide:*` dir loads pages until row count reaches
  `totalRows`; `Show N more…` works; no duplicate rows.
- `url-share.spec.ts`: full state (`left,right,l,r,path,filter`) reproduces
  the view in a fresh page.

## Acceptance criteria

The three RESEARCH Q12 fixes are explicit acceptance criteria:

1. **Sticky, aligned header**: the column header row is present, sticky at the
   top of the tree scroll container, and remains pixel-aligned with row columns
   at every indent depth — verified in e2e by comparing header/cell x-positions
   at the fixture's deep path
   (`/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path/`).
2. **Fixed-width, non-wrapping numeric columns**: every numeric column
   reserves DESIGN §3's worst-case width, right-aligned, mono, tabular-nums,
   `white-space: nowrap`; `−1023.9 MiB` renders without wrap or overflow.
3. **Labelled Δ vs absolute columns**: headers read
   `± | Δ size | Δ files | Size | Files | Rel. size` with title-tooltips per
   DESIGN §5.3; no inline unit-suffix jargon (`393 f` style) anywhere.

Plus:
- `mise run e2e` passes on a machine with no Docker daemon and no network.
- All §9 tree states reachable and styled (spot-check #24–#29 manually; #25,
  #26, #28 asserted in e2e).
- Virtualized tree stays smooth (no layout thrash) on the wide fixture; row
  height fixed 32px.
- Diff semantics visible: color + glyph + hatch for every state (never color
  alone); grayscale screenshot check per DESIGN §7.
- Full golden workflow demonstrable by a human following ARCHITECTURE §9.5
  items 2–10.

## Risks / gotchas

- **Name filter gap (flagged during slicing)**: DESIGN §5.3/§10.5 wants
  server-assisted name search beyond the loaded window, but ARCHITECTURE §6
  defines no search endpoint. Ship the client-side substring filter over
  loaded rows in this phase (matches the approved prototype). If reviewers
  want true server search, that is an API addition — record it in DECISIONS.md
  as a delta rather than inventing an endpoint mid-phase.
- headless-tree expects stable item ids — use absolute paths; drill-down
  re-roots by changing the data source root, not by remounting the world
  (avoid losing scroll/disclosure unnecessarily on filter toggles).
- `aria-setsize` must use post-filter `childCount` from the server, not loaded
  row count.
- Playwright webServer builds the binary first — keep `.e2e-data` gitignored
  and wiped between runs so LRU/state from prior runs can't flake tests.
- Dim-not-blank on selection change (state #24) needs `keepPreviousData`-style
  handling with TanStack Query v5 (`placeholderData: keepPreviousData`) —
  verify it works with `useInfiniteQuery` per directory.
