# Phase 006 — Frontend: app shell, selection view, layer comparison view

## Goal

Build the SPA shell (routing, query client, theme, API client, DESIGN visual
language) and the first two user-facing surfaces: the image-selection view with
A/B slots over the Analyzed source, and the compare page's layer-comparison
section — trunk, fork, branches, dotted could-be-shared edges, instruction
tooltips, selection model, and URL-encoded state — all against the real
phase-005 API. After this phase a user can visually reproduce the left half of
`screenshots/04-browse-light.png` with live data.

## Scope

**In:** app shell (wouter routes `/` and `/compare`, QueryClientProvider,
light/dark theme toggle with DESIGN §2.4 tokens); typed API client mirroring
§6 DTOs; shadcn/ui vendored primitives (button, tabs, tooltip, popover, input);
SelectPage: slot cards + arming/auto-fill/Set A/Set B/toggle-removal (DESIGN
§4.2), source tab bar with the **Analyzed tab functional** (list rows, demo
chip, filter input), Compare button gating; ComparePage: header A/B chips,
LayerGraphPanel (trunk/branch cards per DESIGN §5.2 anatomy, SVG overlay with
fork elbow + dotted edges + midpoint pills + hover pairing + honesty popover),
selection model (radio semantics, trunk sets both, defaults to full stacks),
degenerate-case rendering (no-shared / strict-prefix / identical notes), URL
state codec (`left,right,l,r,path,filter` per §8.3), sticky selection chips;
right column shows a designed placeholder/skeleton ("filesystem diff arrives
in phase 007" is *not* user-visible copy — use DESIGN state #18/#24 skeleton
with the phase-007 panel absent); overflow strategies for every slot touched
(DESIGN §3: ref truncate-start, digest truncate-middle, instruction
truncate-end + raw popover); TS unit tests.

**Not in this phase:** the filesystem diff tree (phase 007); Docker and
Registry tabs, pull progress (phase 008 — the tab bar renders only Analyzed
until then; no dead placeholder tabs); Playwright (phase 007).

## Prerequisites

Phases 001 and 005 (live API to develop against via `mise run dev`).

## Files to create/modify

- `web/src/App.tsx`, `web/src/routes.tsx`, `web/src/theme.tsx`
- `web/src/api/client.ts`, `web/src/api/types.ts` (§6 DTO mirrors),
  `web/src/api/queries.ts` (keys/policies per §8.2 table)
- `web/src/lib/format.ts` + tests (bytes → `14.3 MiB` rules of DESIGN §3;
  signed deltas; compact counts `1.2K`)
- `web/src/lib/urlstate.ts` + tests (§8.3 codec)
- `web/src/lib/instruction.ts` + tests (display cleaning mirror + tooltip raw)
- `web/src/select/SelectPage.tsx`, `SlotCard.tsx`, `SourceTabs.tsx`,
  `AnalyzedList.tsx`
- `web/src/compare/ComparePage.tsx`, `LayerGraphPanel.tsx`, `LayerCard.tsx`,
  `EdgeOverlay.tsx` (hand-rolled SVG per DECISIONS C4), `selection.ts`
  (reducer) + tests
- `web/src/components/ui/*` (vendored shadcn primitives)
- Vitest setup for Testing Library.

## Implementation steps

1. Shell: theme tokens as CSS variables per DESIGN §2.4 (both palettes,
   `--focus-ring`, A/B accents); wouter routes; query client with §8.2
   policies (`staleTime: Infinity` for content-addressed keys).
2. API layer: hand-written TS types matching §6.2/§6.4 exactly (field-for-field
   with the Go DTO structs — a comment cross-links them); fetch wrapper that
   parses the §6.1 envelope into a typed `ApiError`.
3. SelectPage per DESIGN §4.1–4.3: slots with arming pulse
   (`prefers-reduced-motion` respected), auto-fill order, explicit Set A/Set B
   hover buttons, same-image note, Compare gating + helper text; Analyzed list
   rows (ref truncate-start, digest truncate-middle + copy tooltip, size,
   layers, relative time, demo chip); client-side substring filter.
4. ComparePage layer section per DESIGN §5.2: card anatomy (radio dot,
   keyword-bolded instruction, size + relative bar vs `maxLayerBytes`, 0 B
   "empty" case, DiffID mid-truncated with full-digest tooltip); trunk full
   width, branches half; SVG overlay (spine, elbow, branch spines, dotted
   edges `stroke-dasharray 2 5` with `≈ same content` pills; hover thickens +
   lifts partner; click popover with the RESEARCH Q9-approved honesty copy —
   "equivalent under Docker's build-cache rule", never "shared").
5. Selection model: reducer enforcing one point per side, trunk click sets
   both (`l === r`), default full stacks; selection rule labels
   (`A @ layer 6`); radiogroup roles + §7 keyboard map (↑↓ within column, ←→
   across, Space/Enter select, Home/End).
6. URL state: read/write search params; missing `l`/`r` default to full
   stacks; pasting a URL restores pair + selection (tree params `path`/`filter`
   pass through untouched for phase 007).
7. Degenerate cases from `/diff/layers` data: k=0 strip, strict-prefix note,
   identical note (DESIGN §5.2 "Degenerate cases", states #19/#20).
8. Gates (`tsc`, eslint, vitest); commit.

## Test cases (Vitest — the §9.3 slices owned by this phase)

- `format.bytes`: boundaries `1023 B`, `1.0 KiB`, `999 KiB`, `1.02 GiB`,
  `0 B`; signed deltas `+14.3 MiB`/`−2.1 MiB`; compact counts `± 1.2K`.
- `urlstate.roundtrip`: params ↔ state for all fields; defaults when `l`/`r`
  absent (full stacks); malformed values (negative, non-numeric, bad digest)
  fall back safely.
- `selection.reducer`: trunk selection forces `l === r`; branch selection
  moves only its side; out-of-range guarded; default-on-entry rule.
- `instruction.cleaning`: buildkit + classic `#(nop)` forms; unknown mapping →
  italic-unknown path; raw preserved for tooltip.
- `AnalyzedList` (Testing Library): rows render from mocked `['images']`;
  clicking fills armed slot; Set A/Set B override; second click removes;
  Compare disabled until both slots filled.
- `LayerGraphPanel` (Testing Library, mocked `/diff/layers` golden payload):
  trunk cards marked shared, branch cards accent-attributed, dotted edge
  elements present exactly for `couldBeShared` pairs, popover copy says
  "could be shared" and never "shared layer".
- `EdgeOverlay.geometry`: pure function mapping card indexes → SVG path
  endpoints (unit-testable math, no DOM measurement in the test).

## Acceptance criteria

- With the phase-005 server running: selecting `example:v1`/`example:v2` and
  pressing Compare navigates to `/compare?left=…&right=…` and renders trunk +
  fork + branches + two dotted edges matching `screenshots/04-browse-light.png`
  (left column) in both themes.
- Every interactive element passes DESIGN §1.2's three-part rule (resting
  affordance, hover response, pointer cursor); digests/sizes have none.
- Overflow: long refs truncate-start, digests truncate-middle with copy
  tooltip, long instructions truncate-end with raw multi-line popover
  (scrollable, max-height 40vh) — verified with deliberately long mock data.
- Keyboard: full radio-group navigation per DESIGN §7; focus-visible rings.
- URL paste restores pair and selection exactly (shareability criterion §8.3).
- Trunk selection produces `l === r` in the URL (the intentional self-diff
  per RESEARCH Q11).
- `tsc --noEmit`, eslint, vitest all green; no `any` in new code.

## Risks / gotchas

- SVG overlay alignment: measure card positions via refs +
  `ResizeObserver`, not hardcoded offsets — branch columns scroll
  independently (DESIGN §5.1) and long branches (state #23) must keep edges
  attached. Keep geometry math in a pure module so it stays testable.
- Radix Tooltip/Popover + `role=radio` composition: cards are radios *and*
  popover triggers — ensure Space/Enter selects rather than opening the
  popover (separate `≈` chip button is the popover trigger, per DESIGN).
- Don't leak phase-007 params: `path`/`filter` must survive URL round-trips
  even though nothing consumes them yet, or phase 007 inherits a codec bug.
- The tab bar must be built to accept two more tabs (phase 008) without
  layout shift — fixed segmented-control styling now, per DESIGN §4.3.
