# Phase 005 — Cache store, fixture ingestion & analysis API

## Goal

Make the golden-demo *backend* real: a durable on-disk cache with atomic
writes, flock, pinning, LRU accounting/eviction and the cache-full refusal
(RESEARCH Q7); an ingestion path that loads the vendored OCI-layout fixtures at
startup through the phase-002 indexer; and the JSON API that serves images, the
layer graph, and the paginated server-aggregated diff tree. At the end of this
phase, `curl` can walk the entire golden workflow against a fixture-seeded
server with zero network.

## Scope

**In:** `internal/cachestore` (layout §5, staging + fsync + rename commit
order, startup sweep, flock, refcounted layer eviction, pinned images,
`lastUsedAt` debounced Touch, byte accounting, LRU eviction, `cache_full`
refusal); `internal/ingest` *local subset*: OCI-layout source (gcr `layout` /
`tarball` packages), the ingest pipeline that streams layers through
`analyze.IndexLayer` into cachestore with per-layer commit, skip-already-
indexed, and startup fixture loading (pinned); `internal/server`: error
envelope in use, `GET /api/v1/images`, `GET /api/v1/images/{id}`,
`GET /api/v1/diff/layers`, `GET /api/v1/diff/tree` (depth/limit/cursor/filter,
comparison LRU + single-flight), `GET /api/v1/meta`, healthz gated on fixtures
loaded (§1.3).

**Not in this phase:** registry pulls, docker daemon, pull manager/progress
API, imgref/safehttp (all phase 008 — `POST /api/v1/pulls`, `GET /pulls*`,
`GET /docker/images` do not exist yet); any frontend changes.

## Prerequisites

Phases 002, 003, 004.

## Files to create/modify

- `internal/cachestore/store.go`, `layout.go`, `lru.go`, `flock.go` + tests.
- `internal/ingest/ingester.go` (pipeline core: staging → IndexLayer → commit),
  `layout.go` (OCI layout source), `fixtures.go` (startup loader) + tests.
- `internal/server/`: `handlers_images.go`, `handlers_diff.go`, `dto.go`
  (§6.2–§6.5 TS-mirroring structs), `errors.go` (§6.1 envelope + code table),
  `comparison.go` (in-memory comparison LRU, cap 2, single-flight),
  `pagination.go` (opaque cursor codec) + tests.
- `cmd/layerlens/main.go` — wire store/ingester/server; `--fixtures-dir`
  startup load; flock before serving; healthz gating.
- `mise.toml` — no new tasks; e2e-style manual check documented in phase file.

## Implementation steps

1. `cachestore` per §5 exactly: `v1/` layout, digest path validation
   (`^[a-f0-9]{64}$` before Join, §7.3), tmp+fsync+rename discipline,
   layer-dir commit order (`index.jsonl.zst` then `layer.json`), image record
   last, startup sweep of `staging/` and `layer.json`-less dirs, flock
   (`LOCK_EX|LOCK_NB`, fail fast).
2. LRU per §5: byte accounting recomputed at startup + incremental; debounced
   (≥60 s) `Touch`; eviction in `lastUsedAt` order (record removed first, then
   unreferenced layer dirs via refcount over image records); pinned exempt;
   **refusal path**: in-flight image whose own bytes exceed
   `cap − Σ pinned` aborts with `cache_full`, staging deleted, nothing else
   evicted. Cap injectable (RESEARCH Q7 testability).
3. `ingest` local pipeline: given a gcr `v1.Image` from a layout path, iterate
   layers; for each, if DiffID already in store → skip (no streaming); else
   stream `Compressed()` through `analyze.IndexLayer` into a staged layer dir;
   commit per-layer (durable checkpoint unit, §4.1); then MapHistory +
   ChainIDs + ChangesetDigests into `ImageRecord`; commit record. Startup:
   scan `--fixtures-dir` index annotations, ingest any not-yet-present image,
   mark `Pinned`, log, then open healthz.
4. `server` diff endpoints:
   - `/diff/layers`: load two records, `analyze.TrunkLCP` + branches +
     `CouldBeSharedEdges`, DTO per §6.4 (`owner`, `maxLayerBytes`).
   - `/diff/tree`: validate params (`leftLayers`/`rightLayers` are counts,
     0..len; `path` must be clean+rooted; `limit ≤ 1000`; `depth ∈ {1,2}`);
     assemble comparison via comparison LRU keyed by
     `(leftID, rightID, l, r)` with single-flight; walk to `path`; apply
     `filter=changed` (subtree has any change); page per §6.5 cursor
     (`{section, lastName}`, opaque base64, `bad_request` when stale/mismatched);
     compute `maxSiblingBytes` over ALL post-filter siblings; `depth=2`
     includes first-`limit` grandchildren with `childrenTruncated`.
   - `/images`, `/images/{id}`, `/meta` per §6.2/§6.6; Touch on comparison use.
5. Wire `main.go`; verify the §1.2 lifecycle manually with curl.
6. Gates; commit.

## Test cases

Cachestore (`store_test.go`, `lru_test.go` — §9.1 slice):
- `atomic_commit_order_survives_kill`: simulate crash between renames (write
  files in order, delete `layer.json`, rerun sweep) → consistent store, orphan
  swept.
- `image_record_only_after_all_layers`.
- `lru_evicts_in_lastUsedAt_order`; `touch_debounced`.
- `refcounted_layer_survives_until_last_image_gone` (shared DiffID across two
  images; evict one → layer stays; evict both → gone).
- `pinned_never_evicted` even under a cap forcing eviction of everything else.
- `tiny_cap_refuses_with_cache_full_and_preexisting_untouched` (RESEARCH Q7's
  named requirement).
- `concurrent_read_during_eviction_sees_old_or_gone_never_torn`.
- `digest_path_traversal_rejected` (`sha256:../../x` never reaches Join).
- `second_process_flock_fails_fast`.

Ingest (`ingester_test.go` against fixtures):
- `fixture_startup_load_all_pinned`; `startup_idempotent` (second boot ingests
  nothing, byte-stable store).
- `already_indexed_layer_skipped_without_streaming` (counting source asserts
  zero reads for shared trunk layers when ingesting v2 after v1).
- `diffid_mismatch_aborts_image` (corrupt a staged fixture copy).

Server (`handlers_*_test.go`, httptest against a fixture-seeded store):
- `layers_golden_pair`: trunk length, owners, instructions, edges with
  `diffIdEqual=false` for the npm pair; `maxLayerBytes` correct.
- `layers_degenerate_pairs`: strict-prefix, disjoint, identical (self-diff).
- `tree_root_page_shape`: rows sorted dirs-first/name-asc, `TreeAgg` matches a
  hand-computed expectation for the golden pair at full stacks.
- `tree_trunk_point_self_diff_all_unchanged` (l == r ≤ k → every row
  Unchanged; `filter=changed` → empty rows + correct `totalRows`).
- `tree_pagination_wide_dir`: 2,500 children at `limit=500` → 5 stable pages,
  `nextCursor` chain terminates, `maxSiblingBytes` identical across pages,
  union of pages == `totalRows`, no duplicates.
- `tree_stale_or_foreign_cursor_bad_request`.
- `tree_depth2_truncation`: grandchildren capped with `childrenTruncated`.
- `tree_filter_changed_prunes_unchanged_subtrees`.
- `tree_param_validation`: layer counts out of range, unclean path, limit >
  1000 → `bad_request`.
- `comparison_lru_single_flight`: concurrent first requests assemble once
  (instrumented assembly counter).
- `error_envelope_on_all_failures`; `image_not_found_404`.
- `healthz_gated_until_fixtures_loaded`.
- `meta_reports_cache_bytes_and_cap`.

## Acceptance criteria

- `./bin/layerlens --data-dir <tmp> --fixtures-dir fixtures` starts with **no
  network**, logs fixture loading, then `/healthz` → ok (PROJECT.md acceptance
  "start off by loading the pre-specified images", per RESEARCH Q2 reading).
- The §1.2 golden lifecycle works via curl: list images → both examples with
  `source:"fixture"`, `pinned:true` → `/diff/layers` shows trunk + branches +
  two could-be-shared edges → `/diff/tree` at post-fork points shows
  `debug.log` added, `main.js` modified, apt paths removed, with folder
  aggregates and stable pagination.
- Restarting the server preserves analyzed images (durable cache) — verified
  by a test and by hand.
- Payload bound honored: default page (`limit=200, depth=1`) ≤ ~70 KB for the
  wide fixture (assert coarse upper bound in a test).
- A run with `--cache-max-bytes=1000000` refuses an over-budget ingest with
  `cache_full` and leaves prior entries intact (UAT §9.5 item 14, automated).

## Risks / gotchas

- **Commit-order bugs are silent** until a crash; the kill-simulation test must
  really exercise the sweep, not mock it.
- Cursor stability depends on deterministic sort — the shared `sort.go` from
  phase 003 must be the single ordering authority for both Diff children and
  page slicing.
- `filter=changed` totals: `totalRows` and `childCount` are **post-filter**
  (§6.5) — easy to get wrong and it breaks `aria-setsize` and the "N of M"
  chips later.
- Comparison LRU memory: cap 2 per §4.6; releasing side trees after Diff is
  what keeps the §4.6 budget — verify with a coarse runtime.MemStats check in
  a test if cheap, otherwise by review.
- gcr `layout` gives `v1.Image` per manifest — skip non-linux/amd64 and
  attestation (`unknown/unknown`) manifests when walking `index.json`
  (DECISIONS risk 4).
