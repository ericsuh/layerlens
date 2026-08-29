# Phase 003 — Analysis algorithms: squash, diff, aggregate, trunk, edges

## Goal

Complete `internal/analyze` with the pure algorithms that turn per-layer
indexes into everything the product shows: cumulative squashed trees with
correct whiteout/opaque semantics, the unified diff tree with bottom-up
aggregates, the shared-trunk longest-common-prefix computation, and the
could-be-shared edge detection. After this phase the entire analytical brain
of layerlens exists and is exhaustively unit-tested; no I/O yet.

## Scope

**In:** `Squash` (§4.2 two-pass semantics incl. all pinned edge cases), `Diff`
(§4.3, self-contained DiffNode output, tarsum-v1 modification predicate from
§3.2), bottom-up `Agg` in the same post-order walk (§4.4), trunk LCP with all
degenerate cases (§4.5 preamble), could-be-shared edge computation (§4.5),
plus small helpers the server will need verbatim (child sorting: dirs first
then files, name-ascending, per §6.5).

**Not in this phase:** pagination/cursoring and the `changed` filter (server
concerns, phase 005); the comparison LRU (phase 005); any serialization.

## Prerequisites

Phase 002 (domain types, indexer, changeset digest — tests here build
`LayerIndex` values via the phase-002 indexer or literal structs).

## Files to create/modify

- `internal/analyze/squash.go` + `squash_test.go`
- `internal/analyze/diff.go` + `diff_test.go` (includes Agg)
- `internal/analyze/trunk.go` + `trunk_test.go`
- `internal/analyze/edges.go` + `edges_test.go`
- `internal/analyze/sort.go` (shared child-ordering used by Diff and later by server)

## Implementation steps

1. `Squash(indexes []LayerIndex) *Node` following §4.2's pseudocode exactly:
   pass 1 opaque-then-whiteout deletions against lower state only; pass 2
   upserts with `ensureDirs` implicit parents (KindDir, mode 0755,
   `Implicit=true`), dir-re-statement keeps children, kind-change drops/creates
   subtrees, explicit dir entry clears `Implicit`. Encode each §4.2 "Exact
   semantics pinned down" bullet as a distinct code path or comment tied to its
   test.
2. `Diff(l, r *Node) *DiffNode` per §4.3: `markSubtree` for one-sided cases,
   sorted-union merge of children, `metaDiffers` = tarsum-v1 field set (§3.2:
   Kind, Mode 12-bit, UID, GID, Uname, Gname, Size, LinkTarget, Devmajor,
   Devminor, Xattrs, ContentSHA — mtime never), Implicit dirs never
   mode-modified. Copy `SideMeta` so inputs are droppable (§4.3's memory
   property).
3. Fill `Agg` in the same post-order visit per §4.4: regular files only in
   byte/file counts; modified contributes to both sides' byte totals; symlink/
   hardlink/device/fifo contribute 0 bytes and are excluded from Files counts
   but still appear as rows.
4. `TrunkLCP(a, b []Layer) int` — pairwise DiffID equality prefix (never
   compressed digests; DECISIONS A4).
5. `CouldBeSharedEdges(a, b []Layer, k int)` per §4.5: multimap on
   ChangesetDigest over post-fork layers, skip `EntryCount == 0`, emit all
   pairs with `diffIDEqual` flag.
6. Run gates; commit.

## Test cases (the §9.1 slices owned by this phase — the tricky ones by name)

Squash (`squash_test.go`):
- `whiteout_deletes_lower_file` / `whiteout_deletes_lower_subtree` /
  `whiteout_nonexistent_noop`.
- `whiteout_and_recreate_same_layer`: layer both whiteouts `x` and ships `x` →
  the layer's own `x` survives.
- `opaque_clears_lower_children_dir_survives`.
- `opaque_applied_before_own_entries_regardless_of_tar_order`: feed the layer's
  entries with the opaque marker deliberately *after* its sibling files in the
  slice — siblings must survive (two-pass structure is the guarantee under test).
- `opaque_on_absent_dir_noop`; `opaque_plus_explicit_whiteout_same_dir`.
- `file_replaces_dir_drops_subtree` / `dir_replaces_file_fresh_subtree`.
- `implicit_parent_dirs_created` + `explicit_dir_clears_implicit_flag`.
- `restated_dir_keeps_children_updates_meta`.
- `hardlink_size_counted_once`; `dangling_hardlink_after_target_whiteout`
  (link kept as-is, never resolved).
- `duplicate_paths_never_reach_squash` (indexer guarantees; assert via
  integration of indexer→squash on a duplicate-bearing tar).

Trunk (`trunk_test.go`):
- `normal_fork`; `zero_shared_layers` (k=0); `strict_prefix` (k=len(A), empty
  left branch); `identical_images` (both branches empty); `single_layer_images`.

Diff/Agg (`diff_test.go`):
- `added_subtree_marks_recursively` / `removed_subtree` with Agg filled at
  every level.
- `modified_file_counts_both_sides_bytes` (ModifiedBytesLeft/Right).
- `dir_status_modified_iff_descendant_or_own_meta_changed`;
  `implicit_dir_mode_never_flags_modified`.
- `mtime_only_change_is_unchanged` (the §3.2 load-bearing rule).
- `uid_gid_change_is_modified` (Q9 field set in the tree predicate).
- `non_file_kinds_zero_bytes_but_present_as_rows`.
- `agg_invariant_fuzz`: parent.Agg == Σ children over randomized generated
  tree pairs (property test, fixed seed).
- `child_ordering_dirs_first_then_files_by_name`.

Edges (`edges_test.go`):
- `equal_changeset_digests_post_fork_get_edge`; `mtime_differing_layers_edge`
  (different DiffID, equal ChangesetDigest — the npm-install demo case);
- `empty_changesets_excluded`; `m_x_n_duplicate_layers_all_pairs`;
- `diffIdEqual_flag_set_when_tar_identical`;
- `trunk_layers_never_in_edges` (only indexes ≥ k considered);
- `mode_only_change_no_edge` (feeds phase 004's edgecase fixture expectation).

## Acceptance criteria

- All named tests pass; `analyze` still imports only stdlib + `domain`.
- The modification predicate and the changeset digest demonstrably share one
  field-set definition (single source of truth in code — e.g. both call the
  same field-extraction helper), so the §3.2 "never disagree" guarantee is
  structural.
- Squash+Diff of two 6-layer synthetic stacks with all edge cases produces a
  tree that a human can verify against a worked example committed as a test
  (golden expected structure in the test file, not a binary blob).
- Property fuzz (`agg_invariant_fuzz`) runs deterministically under `go test`
  (seeded) and passes.

## Risks / gotchas

- **Opaque ordering is the classic OCI reader bug** — the spec requires
  "applied first, regardless of the ordering in which the whiteout file was
  encountered" (DECISIONS Key facts). The two-pass structure must be the only
  mechanism; do not rely on sorted entry order making it accidentally work.
- Dir "own meta changed" uses **Mode only** (§4.3) and only when neither side
  is Implicit — dirs have no ContentSHA; don't let the shared field-set helper
  accidentally compare dir sizes.
- `markSubtree` must fill Agg on the way — one-sided subtrees have no second
  pass to fix it later.
- Memory: Diff must not retain `*Node` pointers inside DiffNode (SideMeta is a
  copy) or the §4.6 "side trees are transient" budget silently breaks.
