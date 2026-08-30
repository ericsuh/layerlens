# DESIGN.md — layerlens UI design plan

Companion prototype: `.planning/prototype/` (static HTML/CSS/JS, no build step).
Screenshots in `.planning/prototype/screenshots/` show the rendered result in
light and dark themes at 1440×900.

This document is the design authority for the React implementation. Where a
choice depends on backend data, the *semantic* data requirement is stated in
§10 — endpoint names and JSON shapes belong to `ARCHITECTURE.md`.

---

## 1. Design principles

Derived directly from the spec's demands, in priority order:

1. **Room to breathe.** The spec says "don't look too crowded." Concretely:
   the app never puts two unrelated groups closer than 24px; data rows get 8px
   vertical padding minimum; the two main sections of the browse view are
   separated by a 24px gutter; layer cards have 12px internal padding and 14px
   vertical gaps. Density comes from good alignment, not from shrinking gaps.
2. **Interactive means visibly interactive.** Every clickable element must
   read as clickable through *all three* of: (a) resting design (border,
   chevron, radio dot, or button chrome — never bare text), (b) hover response
   (background shift + where appropriate a 120ms transform/elevation), and
   (c) `cursor: pointer`. Non-interactive data (digests, sizes) must *not*
   receive hover styling, so the two classes never blur.
3. **Overflow is designed, not discovered.** Every text slot has a declared
   overflow strategy (§3). No layout may grow horizontally because a filename
   or instruction is long; the container clamps, the strategy handles it.
4. **Never color alone.** Every diff state carries color + a glyph + (on size
   bars and swatches) a hatch pattern (§2.5). This is an accessibility rule
   *and* the spec's explicit "colored and hatched" requirement.
5. **Two identities, always anchored.** Image A and image B each have a fixed
   accent color and a letter badge that appear identically in the header, the
   layer diagram, the selection slots, and the diff legend. The user should
   never have to re-derive "which one is A".
6. **Honest asynchrony.** Anything that can take >300ms shows determinate
   progress when the total is knowable (pulls, indexing — we know layer sizes)
   and a skeleton otherwise. Nothing ever silently spins with no phase label.

---

## 2. Visual language

### 2.1 Typography

System stack; no webfonts (self-contained binary, no network).

| Token | Font | Size/line | Weight | Used for |
|---|---|---|---|---|
| `text-page-title` | UI sans | 20/28 | 650 | View titles ("Compare images") |
| `text-section` | UI sans | 15/22 | 600 | Section headers ("Layer comparison") |
| `text-body` | UI sans | 13.5/20 | 400 | Default UI text, tree rows, layer instructions |
| `text-label` | UI sans | 12/16 | 550, +0.04em tracking, small-caps feel via uppercase | Column labels, badges, source tabs meta |
| `text-meta` | UI sans | 12/16 | 400 | Secondary info (counts, dates) |
| `text-mono` | `ui-monospace, SFMono-Regular, Menlo, Consolas, monospace` | 12/16 | 400 | Digests, paths, image refs, sizes, instructions in tooltips |

Rule: **sizes, digests, refs, and paths are always mono**; tabular numerals
(`font-variant-numeric: tabular-nums`) on every size/count column so bars and
numbers align.

### 2.2 Spacing scale

4px base: `4, 8, 12, 16, 24, 32, 48`. Applications:

- Page gutter: 32px (≥1280px viewport), 24px below.
- Between the two browse-view sections: 24px.
- Section header → content: 16px.
- Layer card internal padding: 12px 14px; vertical gap between layer cards: 14px.
- Tree row: 32px tall (8px padding on 16px line), indent step 20px per depth.
- Form/button clusters: 8px between related controls, 16px between groups.

### 2.3 Radius & elevation

- Radius: 6px (controls, chips, bars), 10px (cards, panels), 999px (badges).
- Elevation: borders first, shadows second. Panels: 1px `--border` +
  `shadow-sm` (0 1px 2px rgb(0 0 0 / .06)). Hovered interactive cards add
  `shadow-md` (0 4px 12px rgb(0 0 0 / .10)) — in dark mode elevation is
  expressed by *surface lightening* (+4% L) instead, shadows are near-invisible.
- Popovers/tooltips: `shadow-lg` (0 8px 24px rgb(0 0 0 / .14)) + border.

### 2.4 Color system — semantic tokens

Neutral base is slate. Both themes are first-class; tokens flip, component CSS
never hardcodes a hex. Contrast ratios below were computed (WCAG 2.x relative
luminance) against the theme's *surface* color and are the shipped values.

**Base (light / dark):**

| Token | Light | Dark |
|---|---|---|
| `--bg` (page) | `#f8fafc` | `#0b1120` |
| `--surface` (panels, rows) | `#ffffff` | `#111a2e` |
| `--surface-2` (inset wells, hover) | `#f1f5f9` | `#1a2540` |
| `--border` | `#e2e8f0` | `#27324d` |
| `--text` | `#1e293b` (14.6:1) | `#e2e8f0` (14.1:1) |
| `--text-muted` | `#64748b` (4.8:1) | `#94a3b8` (6.8:1) |
| `--focus-ring` | `#2563eb` | `#60a5fa` |

**Semantic (foreground on surface / tinted background / bar fill):**

| Token family | Light fg (ratio) | Light tint bg | Dark fg (ratio) | Dark tint bg | Glyph |
|---|---|---|---|---|---|
| `--added` | `#15803d` (5.0:1) | `#dcfce7` | `#4ade80` (10.0:1) | `#12291c` | `+` |
| `--removed` | `#b91c1c` (6.5:1) | `#fee2e2` | `#f87171` (6.3:1) | `#2d1517` | `−` |
| `--modified` | `#a16207` (4.9:1) | `#fef3c7` | `#fbbf24` (10.4:1) | `#2a2110` | `±` |
| `--unchanged` | `#64748b` (4.8:1) | — (no tint) | `#94a3b8` (6.8:1) | — | `·` |
| `--shared` | `#475569` (7.6:1) | `#f1f5f9` | `#94a3b8` (6.8:1) | `#1a2540` | `=` |
| `--image-a` | `#1d4ed8` (6.7:1) | `#dbeafe` | `#60a5fa` (6.8:1) | `#16233f` | `A` |
| `--image-b` | `#7e22ce` (7.0:1) | `#f3e8ff` | `#c084fc` (6.6:1) | `#241b3a` | `B` |

All foregrounds meet WCAG AA (≥4.5:1) for normal text on their surface. One
exception handled explicitly: in light mode, `#a16207` on the amber tint
`#fef3c7` is 4.4:1, so **text rendered on a tinted background uses the -800
shade** (`--added-strong #166534`, `--removed-strong #991b1b`,
`--modified-strong #854d0e`), all ≥5.2:1 on their tints. Bar *fills* use the
fg colors at full strength; tints are backgrounds only.

A/B accents are deliberately blue/purple (not red/green) so image identity can
never be confused with diff polarity, and both survive deuteranopia.

### 2.5 Hatching

Hatch patterns are CSS `repeating-linear-gradient` (no SVG defs needed, works
on any element). They appear on: (a) size-bar segments in the diff tree and
layer list, (b) the legend swatches, (c) the background tint of added/removed
*file* rows (at low alpha). Angles differ so patterns are distinguishable even
in grayscale:

| State | Pattern | CSS recipe |
|---|---|---|
| added | 45° diagonal stripes | `repeating-linear-gradient(45deg, fill 0 3px, transparent 3px 6px)` over 25%-alpha fill |
| removed | 135° diagonal stripes | same, `135deg` |
| modified | vertical ticks | `repeating-linear-gradient(90deg, fill 0 2px, transparent 2px 6px)` over 25%-alpha fill |
| unchanged | none — solid neutral at 35% alpha | — |
| could-be-shared edge | dotted stroke | SVG `stroke-dasharray: 2 5`, `stroke-linecap: round` |

Solid-color (unhatched) bar segments are reserved for "unchanged", so *any*
texture = change. Hatch line thickness ≥2px so it survives HiDPI scaling.

---

## 3. Text overflow strategy — by element type

No element may push layout. Every strategy below includes "full value
recoverable": via title-tooltip, popover, or copy affordance.

| Element | Strategy | Details |
|---|---|---|
| Digests (`sha256:…`) | **Truncate-middle**, fixed width | Render `sha256:ab34…9f21` (7+4 hex). Full digest in a hover/focus tooltip with a copy button. Middle-truncation because head *and* tail are the distinguishing parts users compare. |
| File/dir names in tree rows | **Truncate-end** with ellipsis | `min-width:0` flex child; `text-overflow: ellipsis`. `title` attr + row tooltip shows full name. Never wrap — rows are fixed-height for virtualization. |
| Full paths (breadcrumbs) | **Collapse middle crumbs** | Show root + `…` + last 2 crumbs when > available width; the `…` is a button that opens a menu of the hidden crumbs. |
| Dockerfile instructions on layer cards | **Truncate-end, single line** + popover | Card shows first line, stripped of `/bin/sh -c` / `# buildkit` decoration. Click/hover opens popover with the *raw* multi-line instruction in mono, pre-wrapped, max-height 40vh with internal scroll (heredoc RUNs can be pages long). |
| Image refs (`ghcr.io/org/name:tag`) | **Truncate-start** (`direction:rtl` trick or JS) | The tail (`name:tag`) is the identifying part; leading registry/namespace elides first: `…org/very-long-name:v2`. Tooltip shows full ref. |
| Sizes / counts | **Never truncate, never wrap** | Short by construction (human units) *and* the column reserves the worst plausible rendering, not the current data: `Δ size` fits `−1023.9 MiB` (84px), `Δ files` fits `+999,999` (64px), `Size` fits `1023.9 MiB` (76px), `Files` fits `999,999` (56px), status fits `± 9.9M` (42px) — all right-aligned, mono, tabular-nums, `white-space: nowrap`. Counts ≥1M keep fitting because the status summary uses compact notation (§5.3) and file counts in images are bounded well below 100M. |
| Error messages | Wrap, max 3 lines, then "Show details" disclosure | Never clip an error silently. |
| Tooltips/popovers themselves | max-width 480px, wrap, internal scroll beyond 40vh | |

Human-readable sizes: binary units, one decimal below 100, none at ≥100
(`14.3 MiB`, `999 KiB`, `1.0 GiB`); raw bytes are always whole (`1023 B`);
`0 B` for zero; deltas always signed (`+14.3 MiB`, `−2.1 MiB`). Rounding
carries into the next unit, so `1024` of any unit is never rendered:
1,048,575 B is `1.0 MiB`, not `1024 KiB`.

---

## 4. Image selection view

### 4.1 Layout

Single centered column, max-width 960px, 32px gutters.

```
┌──────────────────────────────────────────────────────────┐
│ layerlens                                    [theme] ☾   │  header, 56px
├──────────────────────────────────────────────────────────┤
│  Compare two images                                      │  page title
│  ┌───────── slot A ─────────┐  ┌───────── slot B ──────┐ │
│  │ A  example:v1            │  │ B  (empty state)      │ │  slot cards
│  │    142 MiB · 8 layers  ✕ │  │ Select an image below │ │
│  └──────────────────────────┘  └───────────────────────┘ │
│              [ Compare layers → ]  (disabled until both) │
│  ─────────────────────────────────────────────────────── │
│  [ Analyzed ] [ Docker daemon ] [ Registry ]   ← tabs    │
│  (source panel: list or form)                            │
└──────────────────────────────────────────────────────────┘
```

### 4.2 The A/B slot interaction (the crux)

Design rule: **the click target always knows its destination before the
click.** Mechanics:

- Two slot cards side by side at top: left bordered/tinted with `--image-a`
  + a filled circular "A" badge; right with `--image-b` + "B" badge. Empty
  slot shows a dashed 2px border, the badge at 40% opacity, and the text
  "Select an image below".
- **Auto-fill order** for plain clicks on a source row: first click fills A,
  second fills B, subsequent clicks replace the *active* slot. The active
  (next-to-fill) slot is visually armed: solid accent border + soft pulse on
  its badge (2s ease, respects `prefers-reduced-motion`).
- Clicking a slot card makes it the active slot (so "replace A" = click slot
  A, then click a row). The armed state is announced (`aria-live="polite"`:
  "Image A slot active").
- **Explicit override:** every source row shows two small buttons on
  hover/focus — `[Set A] [Set B]` in their accent colors — so precise
  placement never depends on arming state. On touch/no-hover they are always
  visible.
- A row already in a slot shows its badge (A or B) inline and its row gets the
  matching left accent bar; clicking it again removes it from the slot
  (toggle), with the slot's ✕ as the second removal affordance.
- Selecting the same image+digest for both slots is allowed (valid use:
  self-diff shows all-shared) but shows an inline note "Both slots contain the
  same image — every layer will be shared."
- `Compare layers →` is a primary button, disabled with reduced opacity +
  `cursor: not-allowed` + helper text "Choose two images to compare" until
  both slots are filled.

### 4.3 Source tabs

Segmented control (three tabs), each with a count or status chip:

1. **Analyzed** — `Analyzed (4)` — cached/pre-analyzed images. Default tab;
   always available; contains the demo images. Row: image ref (truncate-start),
   short digest (truncate-middle), size, layer count, "analyzed 2h ago". Demo
   images `example:v1` / `example:v2` are pinned first with a small "demo" chip.
2. **Docker daemon** — `Docker daemon` with a green dot when the socket is
   reachable, gray dot + "unavailable" when not. Rows as above plus platform;
   a row not yet analyzed shows an "will be analyzed" note — selecting it is
   allowed and kicks off analysis at Compare time (progress UI, §4.4).
3. **Registry** — a mono text input + `Fetch & analyze` button. Below the
   input, static helper text lists the five allowed registries. Validation is
   *pre-flight and inline*: as the user types, a parse of the ref shows the
   resolved registry ("→ ghcr.io ✓ allowed" or "→ registry.example.com — not
   on the allowlist") before any request is made.

Tab panels share one height (min-height 320px) so switching doesn't jump the
page. Lists over ~8 rows get a filter input (client-side substring on ref).

### 4.4 Pull/analyze progress (up to 25 GiB)

Fetching a registry image replaces the source panel's row (and mirrors into
the slot card) with a **progress card**:

- Header: image ref + resolved digest (once the manifest lands) + `Cancel`.
- **Phases**, shown as a step list with the current one active:
  1. `Resolving manifest` (indeterminate, sub-second)
  2. `Downloading & indexing layers` — determinate: manifest gives every
     layer's compressed size up front, so show overall
     `bytes done / bytes total` + `n of m layers`, a real progress bar, and
     current throughput ("38.2 MiB/s"). Per-layer checkmark list (collapsed
     behind "details" beyond 10 layers).
  3. `Finalizing analysis` (short, indeterminate).
- ETA shown once throughput stabilizes (≥5s of samples); phrased softly
  ("about 4 min left").
- `Cancel` is always enabled; canceling returns the slot to empty and the
  panel to its previous state. Partial per-layer indexes are kept server-side
  (resume is free) but the UI does not promise that.
- The user may switch tabs / fill the other slot while a pull runs; the slot
  card carries a compact progress ring so state is never lost from view.
- Data needed: per-phase events, total & per-layer byte counts, bytes
  completed, cancelability (§10).

### 4.5 Per-source empty / error states

Enumerated in the master table (§9), but the design pattern is: an icon-free
centered block inside the panel (no illustrations), 15px title + 13.5px
muted explanation + at most one action button. Errors are specific and
non-leaky ("This image requires authentication, which layerlens does not
support" — never echoing server internals).

---

## 5. Layer browsing view

### 5.1 Layout

Two-column desktop layout (left: layer comparison; right: filesystem diff),
because the primary loop is "adjust selection on the left, read the diff on
the right" — vertical stacking would put cause and effect a screenful apart.

- Header repeats both image identities as A/B chips (ref truncate-start,
  short digest) + "Change images" link back to selection.
- Left column: fixed 400px (360px below 1280px viewport), independently
  scrollable, 24px gutter to the right column.
- Right column: fluid, min 560px, independently scrollable rows under a
  sticky toolbar (breadcrumbs + filter + legend).

### 5.2 Layer comparison section

#### Geometry

A single vertical spine of **shared trunk cards** (full column width),
then a **fork**: an SVG elbow connector splitting into two half-width
sub-columns (A left, B right, matching the header order), each a vertical
stack of branch layer cards. Order: base layer at top, latest at bottom —
matching Dockerfile reading order; the section header carries a small
"base → latest" direction hint.

The SVG layer (absolutely positioned behind the cards) draws:
- trunk spine: 2px solid `--border`-strong line connecting card centers;
- the fork elbow: two quadratic curves from the last trunk card's bottom
  center to each branch column's first card top center, stroked in
  `--image-a` / `--image-b` at 2px;
- branch spines in the same accent at 60% alpha;
- **could-be-shared edges**: dotted 2px cubic curves (`stroke-dasharray 2 5`)
  bowing outside the columns, connecting matched branch cards, in
  `--modified`-neutral gray... — no: in a dedicated `--shared` stroke with a
  midpoint chip (see below).

#### Could-be-shared affordance

The dotted edge alone is not self-explanatory, so it gets three supports:
1. A small pill at the edge's midpoint: `≈ same content`, 12px, on `--surface`
   so it reads over the line.
2. Both connected cards show a `≈` chip; hovering either card or the edge
   highlights the pair (edge thickens to 3px, partner card lifts).
3. Clicking the pill/edge opens a popover explaining precisely: "These layers
   contain identical files (content + permissions; timestamps ignored), but
   the differing `COPY` layer above them broke the shared cache. They could
   have been shared." — wording keeps the spec's ChainID-vs-content honesty:
   never presented as an actual cache hit.

#### Layer card anatomy (trunk card, full width ~370px; branch ~178px)

```
┌────────────────────────────────────────────┐
│ ◉  RUN apt-get update && install… [≈]      │  selection dot · instruction (truncate-end)
│    98.4 MiB ▕██████▎        ▏  sha…4f21    │  size · relative bar · digest (mid-trunc)
│    [shared]                                │  badge: shared / A / B  (trunk only shows "shared")
└────────────────────────────────────────────┘
```

- **Selection dot**: a literal radio affordance (16px circle) at top-left —
  the single strongest "this is selectable" cue. Filled with the image accent
  when selected.
- **Instruction**: `text-body`, decoration-stripped (`# buildkit`,
  `/bin/sh -c #(nop)` removed), leading keyword (`RUN`, `COPY`…) in 550
  weight. Truncate-end; full raw text in popover (§3). Unknown mapping →
  italic muted "instruction unknown".
- **Size + relative mini bar**: bar is 6px tall, width proportional to
  `layer size / largest layer size in either image` (linear; a `log` toggle
  is *not* offered — instead layers <1% of max render a 2px minimum sliver so
  they stay visible). Bar fill: image accent (branch) or `--shared` (trunk),
  solid — hatching is reserved for diff semantics.
  Empty (0 B) layers show "0 B · empty" and no bar.
- **Digest**: DiffID, middle-truncated mono; tooltip shows full DiffID *and*
  compressed digest, each with copy.
- Branch cards are compact: two lines (instruction / size+bar), the A/B
  identity carried by the card's 3px left accent border + column header chip
  rather than a per-card badge (saves width, stays unambiguous).

#### Selection model

- Exactly **one selection per side**. A trunk card selection sets *both*
  sides to that point (one click = "compare at the shared point": an
  intentionally boring all-unchanged diff that proves trunk sharing).
- Selecting a branch card sets that image's point only; the other side keeps
  its selection. Default on entry: last layer of each branch (the "full
  image diff" — the most useful starting view).
- Visual states:
  - **unselected**: surface card, 1px border, radio outline.
  - **hover**: `--surface-2` background, border→accent at 50%, 120ms; cursor
    pointer; card translates 1px up with `shadow-md` (reduced-motion: no
    transform).
  - **selected**: radio filled, 2px accent border, accent tint background at
    8% alpha, and a bold horizontal **selection rule** across the column at
    the card's bottom edge labeled `A @ layer 6` / `B @ layer 7` — making
    "cumulative up to here" spatially literal (everything above the rule is
    included).
  - **focus-visible**: 2px `--focus-ring` outline offset 2px (in addition to,
    never instead of, hover styling).
- Cards are `role=radio` in two `role=radiogroup`s (trunk cards belong to
  both); full keyboard support (§7).

#### Degenerate cases

- **No shared layers** (fork at root): trunk region renders a single
  explanatory strip "No shared layers — these images share no cache" and the
  fork elbow starts from it; columns begin immediately. Everything else
  unchanged.
- **Strict prefix** (A = trunk, B extends): no fork drawn; trunk runs
  straight into B-only cards (full-width cards with B accent), and A's
  selectable range is the trunk itself. A pinned note explains "example:v1
  is fully contained in example:v2's layer stack."
- **Identical images**: all trunk, note "Images are identical at every layer."
- **Long branches (20+ layers)**: the column scrolls (it is independently
  scrollable by design); cards stay full-size — no zoom-out mini-map for MVP.
  The sticky section header shows `A @ 6 / B @ 21` so the current selection
  is visible even when scrolled away; clicking that chip scrolls to the
  selected card.

### 5.3 Filesystem diff section

#### Toolbar (sticky)

```
/ (root) › usr › lib › x86_64-linux-gnu          [Changed only ⌄] [filter…] 
Comparing A @ layer 6 (…v1)  vs  B @ layer 7 (…v2)      + added − removed ± modified
```

- **Breadcrumbs** implement drill-down: clicking a directory *name* (not its
  disclosure triangle) re-roots the view to that directory; crumbs navigate
  back up; root crumb `/`. Middle crumbs collapse per §3. Re-rooting is
  animated with a 150ms fade+4px slide (reduced-motion: cut).
- **Filter menu**: `All entries / Changed only (default) / Added / Removed /
  Modified`. "Changed only" default because unchanged is the overwhelming
  majority and the spec's purpose is the diff; the menu chip always shows the
  active filter so hidden data is never a mystery. Only the first two are
  server filters; the three polarities narrow the same response client-side.
  A count chip shows "showing 214 of 2,500 entries" — loaded vs total **for
  the current directory**, which is the number a windowed API can produce and
  the one that answers "is this directory fully paged in?".
- **Name filter** input: substring match on names beneath the current root;
  matches auto-expand ancestor chain; debounce 150ms. Searches everything
  *fetched* — including directories that are only prefetched, not expanded —
  and says so in its empty state. Server-assisted search beyond that (§10.5)
  needs an endpoint the API does not yet define; see DECISIONS, phase 007.
- **Legend**: the three glyph+swatch(hatched) pairs; hover explains each.

#### Tree columns & header

Fixed 32px row height (virtualized). One shared column grid — used by the
header row *and* every tree row — so the two can never drift:

```
[ Name (fluid) ][ ± 30px ][   Size 116px   ][ Δ size 84px ][ Files 56px ][ Δ files 64px ]
   [indent][▸][name……]    ±     1023.9 MiB      −1023.9 MiB     999,999       +999,999
                                ▕██▓▓░░▏
```

- **Header row**: persistent and **sticky** (`position: sticky; top: 0`) at
  the top of the tree's scroll container, opaque surface background +
  bottom border so rows scroll beneath it. `text-label` styling (uppercase,
  muted). It is `aria-hidden` — the tree is `role=tree`, not a table, and
  every row's SR text already spells out each value with its meaning (§7).
- **Alignment invariant**: indentation (guides + chevron) lives *inside* the
  fluid Name cell; every numeric column is a fixed grid track. The header
  therefore stays aligned with the rows at every depth by construction —
  there is nothing depth-dependent outside the Name cell.
- **Column set** — `Name | ± | Size | Δ size | Files | Δ files`: each
  absolute is immediately followed by its own delta, so the pair reads as one
  unit ("13.3 MiB, of which +4.8 MiB is new") instead of asking the eye to
  jump two columns to pair them up. There is **no separate relative-size
  column**: the bar lives inside the Size cell, which is the number it
  visualizes — two columns for one quantity was width spent twice. Absolute
  columns are the **B-side** totals (the after state) — keeping the existing
  decision that A-side totals live in the row tooltip. Two labeled absolute
  columns replace the old `142 MiB · 393 f` composite cell: the unit-suffix
  jargon (`f`) is gone, and delta vs absolute is disambiguated by the
  headers, not by inline suffixes.
- **Header labels + tooltips** (label short, meaning in `title`):
  - `±` — "Change status: + added, − removed, ± modified or contains changes,
    · unchanged." **Glyph only, no count**: a directory row reading `± 66` was
    read as a size or a file count as often as a descendant tally. The number
    survives as the cell's tooltip and in the row's SR sentence.
  - `Size` — "Total size in image B, with a bar scaled against the largest
    top-level entry — hover a row for the A-side totals."
  - `Δ size` — "Change in total size, B relative to A."
  - `Files` — "Total file count in image B — hover a row for the A-side totals."
  - `Δ files` — "Change in file count, B relative to A."
- **Fixed widths** are sized from the worst plausible content, not the
  fixture (§3): nothing in a numeric column may ever wrap or overflow.
  Numeric cells are right-aligned, mono, tabular-nums, `white-space: nowrap`.

#### Tree rows

- **Disclosure triangle**: 16px hit area 24px, rotates 90° in 120ms when
  open; only on directories; `cursor:pointer`. **The chevron expands the
  subtree in place; clicking the directory's *name* re-roots the view onto
  it.** Two affordances, each obvious from its shape — a triangle discloses, a
  name navigates — which retires the `↳` "open as root" button that had to be
  explained. Files: no triangle, name click selects the row (shows a detail
  line: full path, mode, per-side sizes).
- **Name**: mono for files, sans for directories (dirs get trailing `/`);
  truncate-end.
- **Status glyph + color** (never color alone): `+` added, `−` removed,
  `±` modified, `·` unchanged, in the status column and colored per token.
  Screen-reader text spelled out (§7).
- **Δ size / Δ files**: signed values colored per polarity — `+14.3 MiB`,
  `+312`. Size units stay (they *are* the value); the ` files` word and any
  other unit suffix are dropped — the column header carries the meaning.
  Zero deltas render as muted `—`. File rows leave `Δ files` empty (a file
  is not a subtree; its add/remove is already the status glyph).
- **Size / Files**: the *B-side* cumulative subtree totals (the "after"
  state) in two labeled columns, with the A-side totals in the row tooltip
  (`A: 98.4 MiB (393 files) → B: 119 MiB (453 files)`) on both cells;
  rationale: one side of truth plus deltas beats four columns of numbers.
  File rows show their own size and leave `Files` empty. Removed entries
  show their A-side values in both columns, struck through.
- **Size bar**: 5px, right-aligned fixed 104px track, positioned inside the
  Size cell beneath its number (absolutely, so it cannot push that number off
  the baseline the other numeric columns share). Width = subtree size /
  **the largest entry at the top level of the visible tree** — one denominator
  for the whole tree, not one per directory. That is what makes a child's bar
  never exceed its parent's: a subtree's bytes are a subset of its parent's,
  so a shared scale orders them by construction. Per-sibling normalization
  re-stretched every level to full width, so a 3 KiB file inside a 4 MiB
  folder drew the same bar as the folder. Segmented fill: unchanged
  portion solid neutral 35% alpha, added portion green 45° hatch, removed
  red 135° hatch (rendered from the A-side share), modified amber
  vertical-tick hatch. Bars <2px clamp to 2px.
- **Row treatment by state:**
  - *File added/removed*: full-row background tint (`--added`/`--removed`
    tint at 60% opacity) + 3px left edge in the fg color; removed names also
    struck through at 70% opacity.
  - *File modified*: no full tint; `±` glyph + amber name color + delta.
  - *Directory itself added/removed* (didn't exist on the other side): same
    full-row tint as files + glyph on the *dir row*, and its children
    inherit their own explicit states (all added/removed).
  - *Directory merely containing changes*: **no tint** — surface row, dir
    name in `--text`, but the status column shows a small stacked-dot
    change summary `± 12` in muted amber meaning "12 changed descendants"
    (counts ≥1,000 render compactly — `± 1.2K`, `± 9.9M` — so the 42px
    status column never overflows; the exact count is in the tooltip),
    and its size bar shows the hatched change segments. This is the visual
    rule that separates "changed container" from "changed thing".
  - *Unchanged*: muted name (`--text-muted`), `·` glyph, no bar texture.
- Depth: indent guides (1px vertical lines at each indent stop, `--border`)
  keep deep rows readable; beyond depth 8 the guides fade and users are
  expected to drill down (breadcrumbs stay shallow by re-rooting).
- Hover: `--surface-2` row background; the whole row is a click target only
  where stated (chevron/name/drill icon) — dead zones do not get pointer
  cursor.

#### Loading & pagination inside the tree

Expanding an unloaded directory shows 3 skeleton rows (shimmering 60%–90%
width bars) in its slot; children stream in sorted (dirs first, then files,
each by descending subtree size — the size-first sort is the tool's point).
Directories over the server page size get a final `Show 1,000 more…` row
(button-styled, full width).

---

## 6. Interaction & motion

| Event | Treatment |
|---|---|
| Hover (buttons, rows, cards) | bg → `--surface-2` (or accent tint), 120ms ease-out; cards add 1px lift + `shadow-md` |
| Active/press | scale 0.985, 80ms; no lift |
| Focus-visible | 2px `--focus-ring` outline, 2px offset, zero-duration (instant) |
| Selection change (layer) | selected card ring animates in 150ms; the diff panel *keeps old content dimmed* (60% opacity + non-interactive) with a top progress bar until new data lands — never a blank flash |
| Disclosure | chevron rotate 120ms; subtree height animates only ≤12 children (measured), else instant (virtualized lists don't height-animate) |
| Drill-down / breadcrumb | 150ms fade + 4px directional slide |
| Skeletons vs spinners | skeleton when layout is known (tree rows, lists, slot cards); spinner (16px, 0.8s) only inside buttons and tiny inline waits; determinate bar whenever totals are known (pulls) |
| Slow request (>10s beyond pulls) | inline note "Still working — large image analysis can take a few minutes" + Cancel where cancelable |
| Reduced motion | `prefers-reduced-motion: reduce` disables transforms, pulses, slides; opacity fades ≤100ms remain |

All durations ≤150ms except progress animations. Nothing loops except
skeleton shimmer and the armed-slot pulse.

---

## 7. Accessibility

- **Layer diagram**: two `role=radiogroup`s ("Image A comparison point",
  "Image B comparison point"); each card `role=radio` + `aria-checked`.
  Trunk cards are members of both groups (implemented as one composite
  widget with `aria-describedby` noting "shared layer — sets both points").
  Keyboard: Tab enters the group at the selected card; ↑/↓ move within a
  column; ←/→ jump between A and B columns at the nearest index; Space/Enter
  selects; Home/End jump to first/last. The could-be-shared edge popover is
  reachable: the `≈` chip on each card is a focusable button.
- **Tree**: `role=tree` / `role=treeitem` with `aria-level`,
  `aria-setsize`, `aria-posinset`, `aria-expanded` (dirs only) — this is
  native to @headless-tree. Keyboard: ↑/↓ move, → expands/enters, ← collapses/
  exits to parent, Enter drills down on a dir / selects a file, Backspace or
  the breadcrumb (Shift+Tab reachable) goes up a root, type-ahead jumps by
  name.
- **Row SR text**: composed as e.g. "node_modules, directory, modified
  contents: 312 files added, 14.3 mebibytes added, total 142 mebibytes,
  level 2, 5 of 9". Status is *words*, never glyph-only; bars are
  `aria-hidden` (their data is in the text), and so is the sticky column
  header (§5.3) — `role=tree` has no column semantics, so the header is a
  purely visual legend whose meanings the row SR text already carries.
- **Focus order**: header → slot A → slot B → Compare → tabs → panel content
  (selection view); header → layer groups (A then B) → toolbar → tree
  (browse view). No focus traps; popovers return focus on close.
- **Live regions**: slot arming, pull progress milestones (25/50/75/done),
  "diff updated for A @ layer 6" after selection change — all
  `aria-live=polite`.
- **Not color alone**: covered by glyphs + hatching (§2.5); verified in the
  prototype by grayscale screenshot check.
- Contrast: §2.4 values; disabled controls are exempt but still ≥3:1 borders.

---

## 8. Responsive behavior

**Desktop-first**, target 1440×900; designed floor **1024×768** (a laptop);
below that we do not optimize (no mobile).

- ≥1280px: browse view two columns (400px + fluid), page gutter 32px.
- 1024–1279px: left column narrows to 360px (branch cards drop the digest
  line into the tooltip), gutter 24px, tree hides both file-count columns
  (`Δ files` and `Files`) in rows *and* header (data remains in the row
  tooltip/SR text); the size columns and their headers stay.
- <1024px (unsupported but non-broken): columns stack vertically — layer
  section first at natural height (own scroll capped 50vh), diff below;
  a floating "A @ 6 · B @ 7" chip keeps selection context while scrolled
  into the tree.
- Selection view is a single centered column throughout; slot cards stack
  under 720px.
- The tree's name column is the only fluid column; all numeric columns are
  fixed-width so narrowing squeezes names (which have an overflow strategy),
  never numbers.

---

## 9. Empty / loading / error / overflow states — master table

| # | Where | State | Treatment |
|---|---|---|---|
| 1 | Analyzed tab | empty (fresh install minus demos — shouldn't happen; demos always seed) | "No analyzed images yet" + pointer to other two tabs |
| 2 | Analyzed tab | loading | 4 skeleton rows |
| 3 | Analyzed tab | cache entry corrupt/evicted mid-view | row shows "re-analysis required" chip; selecting triggers re-analyze flow with progress |
| 4 | Daemon tab | no socket | gray status dot; panel: "No Docker socket found at /var/run/docker.sock — the daemon source is unavailable on this server." No action button (nothing the user can do in-app) |
| 5 | Daemon tab | socket present, zero images | "The Docker daemon has no images." |
| 6 | Daemon tab | daemon error (perm denied, timeout) | specific message + `Retry` |
| 7 | Registry input | ref fails to parse | inline field error under input, red border, "Not a valid image reference" |
| 8 | Registry input | registry not on allowlist | inline: "`registry.example.com` isn't on the allowlist. Allowed: Docker Hub, GHCR, GCR, ECR, ACR." Button stays disabled |
| 9 | Registry fetch | image/tag not found | "`foo:tag` was not found on ghcr.io" + Retry/edit |
| 10 | Registry fetch | requires auth (private) | "This image requires authentication. layerlens supports anonymous public pulls only." Non-leaky, no credential prompt |
| 11 | Registry fetch | rate-limited (Hub) | "Docker Hub rate limit reached — try again later" + retry-after if known |
| 12 | Registry fetch | network failure mid-pull | progress card → error state, `Retry` resumes (server keeps per-layer checkpoints) |
| 13 | Registry fetch | image exceeds size/cache cap | "This image (31.2 GiB) exceeds the server's cache budget" |
| 14 | Pull | in progress | §4.4 phased determinate progress + cancel |
| 15 | Pull | canceled | slot cleared, toast "Pull canceled", panel restored |
| 16 | Slot cards | one/both empty | dashed border, dimmed badge, "Select an image below"; Compare disabled with helper text |
| 17 | Slots | same image both | allowed + inline note (§4.2) |
| 18 | Layer view | analysis still finalizing | full-section skeleton (trunk of 3 + fork + 2×2 ghost cards) with phase label |
| 19 | Layer view | no shared layers | explanatory strip, fork from root (§5.2) |
| 20 | Layer view | strict-prefix / identical images | notes per §5.2 |
| 21 | Layer view | history↔layer mapping unknown | per-card italic "instruction unknown"; layout unaffected |
| 22 | Layer view | 0-byte layer | "0 B · empty", no bar |
| 23 | Layer view | 20+ layer branch | scroll + sticky selection chips (§5.2) |
| 24 | Diff tree | computing after selection change | old content dimmed + top progress bar (§6) |
| 25 | Diff tree | no differences at selected points | centered "No differences — the filesystems are identical at these layers" + hint to select post-fork layers |
| 26 | Diff tree | "Changed only" filter yields nothing in this dir | inline row "No changes in this directory — 1,204 unchanged entries hidden" + button "Show all" |
| 27 | Diff tree | dir expand fails / request error | inline error row + `Retry` in place of children |
| 28 | Diff tree | >page-size children | `Show 1,000 more…` row (§5.3) |
| 29 | Diff tree | name filter no matches | "No entries match 'foo' under /app" |
| 30 | Global | server unreachable (SPA lost backend) | top banner "Lost connection to layerlens server — retrying…", auto-retry with backoff |
| 31 | Everywhere | long text | per-type strategies (§3) |

---

## 10. Data the backend must supply (semantic requirements — no endpoint/field names)

1. **Image summary** per image: reference, resolved digest, platform, total
   size, layer count, analysis timestamp/state.
2. **Layer list** per image, ordered base→top: DiffID, compressed digest,
   uncompressed size, mapped Dockerfile instruction (raw + display-stripped)
   or an explicit "unknown" marker, empty-layer flag.
3. **Trunk/fork computation** (shared DiffID prefix length) and, for each
   post-fork layer pair, whether their **normalized changeset digests** match
   (drives dotted edges) — computed server-side; the client must not diff
   digests itself.
4. **Cumulative diff tree, windowed**: given (image A, layer index a, image
   B, layer index b, directory path, filter, page cursor): the directory's
   children with, per child: name, kind, diff status
   (added/removed/modified/unchanged/contains-changes), subtree byte totals
   for **both** sides, subtree file counts for both sides, byte and count
   deltas, and a child count (for `aria-setsize` and "N more" rows).
   Sorted server-side (dirs first, size desc). Also per-request: total vs
   shown entry counts for the filter chip.
5. **Name search** beneath a root: matching paths + the ancestor chains
   needed to expand to them (bounded result count).
6. **Pull/analyze progress**: subscribable per-operation stream/poll with
   phase, per-layer sizes and completion, overall bytes done/total, and a
   cancel operation.
7. **Source listings**: analyzed-cache list; daemon availability + image
   list; registry ref validation verdict (parsed registry, allowlisted?)
   cheap enough to call per keystroke (or a client-side mirror of the
   allowlist rules — server remains authoritative at fetch time).
8. All sizes in bytes (client formats); all lists paginated; errors carry a
   machine-readable kind matching the states in §9.

---

## 11. Prototype notes (what `.planning/prototype/` demonstrates)

- Both views, switchable; theme toggle (light/dark) top right.
- Fixture data mirrors the spec's demo: `example:v1` vs `example:v2`,
  5 shared trunk layers (node:24 base chain + WORKDIR), fork at `COPY . .`
  (v2 includes `.git/`, `debug.log`, `.env` that `.dockerignore` should have
  excluded), then `RUN npm install` and the apt/ffmpeg layer — both
  content-identical across images → two dotted could-be-shared edges.
- The cumulative filesystem is *computed* in the prototype (layers applied up
  to each selected point, then diffed), so layer selection genuinely changes
  the tree — including the all-unchanged trunk-point case.
- Tree supports disclosure, drill-down with breadcrumbs, changed-only filter,
  tree-normalized hatched size bars inside the Size column, and the sticky header
  (§5.3); the fixture includes the deep path
  `/app/node_modules/@babel/plugin-transform-runtime/lib/get-runtime-path/`
  to prove header/row column alignment holds at depth.
- Simplifications vs this spec: no virtualization, no keyboard nav, no
  popovers (title tooltips only), pull progress is a static mock, and the
  degenerate fork cases are not interactive (described in §5.2 only).
