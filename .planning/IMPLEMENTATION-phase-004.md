# Phase 004 — Fixture generator & vendored demo images

## Goal

Build `cmd/genfixtures`, the committed, deterministic Go generator that writes
the five OCI image-layout pairs of ARCHITECTURE §9.2, and vendor its outputs
under `fixtures/`. These layouts are the demo material required by PROJECT.md
(the `.dockerignore` cache-invalidation lesson) and the test substrate for
every later phase — cachestore, server API, and all Playwright e2e run against
them with zero Docker and zero network (RESEARCH Q2).

## Scope

**In:** the generator (deterministic tars: fixed timestamps, fixed uid/gid,
sorted entries, gzip with zeroed mtime/OS byte; valid OCI layout: `oci-layout`,
`index.json`, blobs, per-image manifest + config with correct `rootfs.diff_ids`
and `history` incl. `empty_layer` entries); all five pairs of §9.2; a
`mise run genfixtures` task; vendored outputs committed; property tests that
validate the fixtures *through the phase 002–003 code*.

**Not in this phase:** loading fixtures into the server (phase 005); any
resemblance to real registry blobs beyond spec validity.

## Prerequisites

Phases 002–003 (validation tests consume the indexer, trunk, and edge code).

## Files to create/modify

- `cmd/genfixtures/main.go` — CLI: `genfixtures --out fixtures/`.
- `cmd/genfixtures/gen/` — deterministic tar/gzip writer, layout writer,
  fixture definitions (one Go file per pair, data-driven: each layer is a list
  of entry specs).
- `cmd/genfixtures/gen/gen_test.go` — determinism + property tests.
- `fixtures/…` — committed layouts (small: kilobytes to a few MiB total).
- `mise.toml` — `[tasks.genfixtures] run = "go run ./cmd/genfixtures --out fixtures"`.
- `.gitattributes` — mark blob files binary.

## Implementation steps

1. Deterministic writers first: tar entries in sorted order, `Uid/Gid`, `Mode`,
   `ModTime` fixed per entry spec (mtime *varies between v1/v2 where the story
   requires it* — that is the point — but is fixed per build); gzip via
   `gzip.NewWriterLevel` with `Header{ModTime: time.Time{}, OS: 255}` so bytes
   are stable across runs and platforms.
2. OCI layout writer: blobs dir, config (with `rootfs.diff_ids` computed from
   the written tars, `history` including `empty_layer: true` entries such as
   `WORKDIR /app` so phase 002's mapping is exercised end-to-end), manifest
   (`linux/amd64`), `index.json` with `org.opencontainers.image.ref.name`
   annotations for tags. Validate with gcr `layout.ImageIndexFromPath` +
   `validate.Image` in tests (test-only gcr import is acceptable; generator
   itself may use gcr's `mutate`/`empty` if simpler — it is a dev tool, the
   §2 import rules bind `internal/` only).
3. Define the five pairs (ARCHITECTURE §9.2, DESIGN §11 shape):
   1. `example:v1` / `example:v2` — golden demo: identical synthetic node-base
      trunk (several layers + an `empty_layer` WORKDIR history entry), diverging
      `COPY . .` (v2 adds `debug.log`, `.env`, `.git/` junk and a modified
      `main.js`), then **byte-different but content-identical `npm install`
      layers** (same entries, different mtimes ⇒ different DiffIDs, equal
      ChangesetDigests) and matching apt/ffmpeg layers containing `.wh.`
      whiteouts (the `rm -rf /var/lib/...` cleanup).
   2. `prefix:base` / `prefix:extended` — strict prefix, one empty branch.
   3. `disjoint:a` / `disjoint:b` — zero shared layers.
   4. `edgecase:opaque` / `edgecase:plain` — opaque dir, dir→file type change,
      hardlinks, symlink retarget, and a **mode-only change** (must show as
      modified in the tree but must NOT produce a dotted edge... note: a
      mode-only difference changes the tarsum digest too, so the layers differ
      under both rules — the assertion is: modified in tree, no edge, exactly
      as §9.2 states).
   5. `wide:v1` / `wide:v2` — one directory with 2,500 children for pagination.
4. Generate, commit outputs, wire the mise task.
5. Tests, gates, commit.

## Test cases

- `determinism_regenerate_byte_identical`: run the generator twice into temp
  dirs → recursive byte equality; and regenerating over the committed
  `fixtures/` yields no git diff (this is the review guarantee of RESEARCH Q2).
- `layouts_valid_oci`: every image loads via `layout.ImageIndexFromPath` and
  passes gcr `validate.Image`; platform is linux/amd64.
- `diffids_match_config`: streaming each layer through phase-002 `IndexLayer`
  with the config's declared DiffID succeeds (self-consistency).
- `example_pair_properties` (through phase 002–003 code):
  - trunk LCP equals the designed trunk length;
  - the two npm layers: DiffIDs differ, ChangesetDigests equal ⇒ exactly the
    designed could-be-shared edges appear;
  - v2's COPY layer adds `debug.log`/`.env`, modifies `main.js`;
  - apt layer whiteouts delete the expected paths after squash.
- `prefix_pair_properties`: k == len(base); left branch empty.
- `disjoint_pair_properties`: k == 0.
- `edgecase_pair_properties`: squash shows opaque clearing + type change +
  dangling/valid hardlinks; mode-only file → StatusModified and **no** edge.
- `wide_pair_properties`: the wide dir has exactly 2,500 children.
- `history_mapping_exercised`: `example:*` configs include `empty_layer`
  entries and MapHistory returns `ok=true` with the designed instructions.

## Acceptance criteria

- `mise run genfixtures` regenerates `fixtures/` with zero git diff.
- `fixtures/` committed and small (< ~20 MiB total; keep layer contents toy-
  sized — the wide pair uses tiny files).
- All property tests pass using only phase 002–003 code paths (no bespoke
  parsing in tests).
- Fixture definitions are data-driven and reviewable (entry specs in Go
  literals, not opaque blobs — RESEARCH Q2's "reviewed, not opaque").

## Risks / gotchas

- **Gzip determinism**: default gzip headers embed mtime and OS — must be
  zeroed/fixed or the "no git diff" acceptance fails on other machines.
- Deliberate mtime skew between the npm layers must live in *entry* mtimes
  (inside the tar), not the gzip header, to change DiffID while keeping the
  changeset digest equal — this is the single most load-bearing fixture
  property; its test (`example_pair_properties`) is the guard.
- `index.json` ref-name annotations are how phase 005 discovers tags — pick
  the annotation convention now (`org.opencontainers.image.ref.name` =
  `example:v1`) and document it in the generator.
- Don't make trunk layers huge for realism; ARCHITECTURE §10.8 explicitly
  accepts toy-scale sizes.
