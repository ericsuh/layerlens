# Phase 002 — Domain model & streaming layer indexer

## Goal

Build the pure core that everything else stands on: the `domain` types, the
one-pass streaming tar indexer with whiteout recognition and per-file content
hashing, the tarsum-v1 normalized changeset digest, history→layer instruction
mapping, path sanitization, and the JSONL+zstd index codec. All of it is
HTTP-free and gcr-free (ARCHITECTURE §2 dependency rules) and fully covered by
table-driven tests fed from in-memory tars — this phase is where 25 GiB support
is structurally decided (stream once, hold only metadata).

## Scope

**In:** `internal/domain` (Digest, ImageRef, ImageRecord, Layer, HistoryEntry,
Entry/EntryKind, LayerIndex, Node, DiffNode/SideMeta/Agg, boundary interfaces
per §2.1/§3); `internal/analyze`: `MapHistory` + instruction cleaning (§4.0),
`sanitizePath` (§7.3 tar-name rules), `IndexLayer` (§4.1: media-type
decompression gzip/zstd/none, counting reader, DiffID verification, whiteout &
opaque markers, xattr filtering, content SHA-256 while draining, last-wins,
sorted output), the normalized `ChangesetDigest` (§3.1, RESEARCH Q9), ChainID
computation (DECISIONS A4 formula); `internal/index` JSONL+zstd codec (§5:
header line, schema version, truncation detection).

**Not in this phase:** squash/diff/aggregation/trunk/edges (phase 003); any
disk cache layout (phase 005); gcr/moby imports (`ingest` adapts streams to
this pure indexer later, per §2.1); fixtures; HTTP.

## Prerequisites

Phase 001 (toolchain gates).

## Files to create/modify

- `internal/domain/digest.go`, `ref.go`, `image.go`, `entry.go`, `tree.go`,
  `interfaces.go`, `errors.go` (`ErrNotIndexed`, `ErrNotFound`).
- `internal/analyze/history.go` + `history_test.go`
- `internal/analyze/sanitize.go` + `sanitize_test.go`
- `internal/analyze/indexer.go` + `indexer_test.go`
- `internal/analyze/changeset.go` + `changeset_test.go`
- `internal/analyze/chainid.go` + `chainid_test.go`
- `internal/index/codec.go` + `codec_test.go`
- `go.mod`: add `github.com/klauspost/compress` (zstd), `github.com/stretchr/testify`.
- Test helper: `internal/analyze/tartest/tartest.go` — builds in-memory tars
  (regular files, dirs, symlinks, hardlinks, whiteouts, PAX xattrs, duplicate
  entries, deliberate ordering) used by phases 002–005.

## Implementation steps

1. Domain types verbatim from ARCHITECTURE §3/§3.1/§3.2, including the
  "which identifier is used where" invariants as doc comments. `Digest`
  validated against `^sha256:[a-f0-9]{64}$` at construction.
2. `sanitizePath` per §7.3: reject NUL, strip leading `/` and `./`,
   `path.Clean`, reject empty/`.`/`..`-containing results; return
   `(clean string, ok bool)` and let callers record `LayerIndex.Warnings`.
3. `MapHistory` per §4.0 pseudocode exactly (empty_layer cursor, count-mismatch
   → `ok=false`, never misalign). Cleaning: strip `/bin/sh -c #(nop) `,
   `/bin/sh -c `, trailing ` # buildkit` (DECISIONS A5); keep raw.
4. `IndexLayer` per §4.1 pseudocode: implement as the pure indexer of §2.1
   (consumes `io.Reader` + media type + declared DiffID). Follow the §4.1
   entryFrom comment for xattrs (keep `security.capability`, drop other
   `security.*`/`system.*` PAX records, mirroring BuildKit).
5. **ChangesetDigest — resolve the §3.1 discrepancy in favor of the binding
   rule.** RESEARCH Q9 (binding) and ARCHITECTURE's own prose/§4.1/§9.1 define
   tarsum-v1 field selection: hash sorted entries over
   `(Path, Kind/typeflag, Mode 12-bit, UID, GID, Uname, Gname, Size,
   LinkTarget, Devmajor, Devminor, sorted Xattrs, ContentSHA)`, **excluding
   only mtime**, with a scheme-version byte prefix and length-prefixed fields.
   The stale snippet in §3.1 listing only `(Path, Kind, ContentSHA, Mode,
   LinkTarget)` and excluding UID/GID contradicts Q9 — implement Q9, fix the
   §3.1 snippet, and note the delta in DECISIONS.md (workflow rule).
6. ChainID per DECISIONS A4: `ChainID(L0)=DiffID(L0)`;
   `ChainID(..Ln)=sha256(prev + " " + DiffID(Ln))` in OCI canonical form.
7. `index` codec per §5: zstd stream of JSON lines, line 1 header
   `{"v":1,"diffId","entryCount"}`, one Entry per line in path order; reader
   rejects unknown major version and detects truncation (entryCount mismatch /
   zstd stream error).
8. Run gates; commit.

## Test cases (table-driven, testify — the §9.1 slices owned by this phase)

History mapping (`history_test.go`):
- `empty_layer_offsets`: ENV/CMD/WORKDIR interleaved between RUNs map correct
  diff_ids to correct instructions.
- `more_history_than_layers` and `more_layers_than_history`: both →
  `ok=false`, all layers `InstructionKnown=false` (never a misaligned guess).
- `buildkit_cleaning`: `RUN /bin/sh -c npm install # buildkit` → `RUN npm install`
  (display), raw preserved. `classic_nop_cleaning`: `/bin/sh -c #(nop) COPY ...`.

Sanitize (`sanitize_test.go`):
- rejects `../etc/passwd`, `a/../../b`, `a/..`, embedded NUL, empty, `.`;
- normalizes `./foo`, `/foo`, `foo//bar`, trailing `/`; accepts deep normal paths.

Indexer (`indexer_test.go`, via tartest in-memory tars):
- `whiteout_entry_captured`: `.wh.file` → `KindWhiteout` with Path = target path.
- `opaque_entry_captured`: `dir/.wh..wh..opq` → `KindOpaque` with Path = dir.
- `duplicate_path_last_wins`: same path twice in one tar → one entry, second's
  metadata.
- `hardlink_size_once`: hardlink entry has Size 0, LinkTarget cleaned.
- `content_sha_streams`: file content hashed correctly; nothing written to disk
  (indexer takes only readers — structurally guaranteed, assert entry ContentSHA).
- `diffid_mismatch_fails`: declared DiffID ≠ stream hash → error (tamper detection).
- `gzip_zstd_none_media_types`: identical tar via all three media types →
  identical LayerIndex.
- `pax_long_names_and_xattrs`: >100-char path via PAX; `SCHILY.xattr.security.capability`
  kept, `SCHILY.xattr.system.foo` dropped.
- `bad_entry_warned_and_skipped`: `..`-traversal name → entry skipped, warning
  recorded, rest of layer indexed.

Changeset digest (`changeset_test.go`):
- `mtime_only_diff_equal`: identical entries, different mtimes ⇒ **equal**
  digests (the product thesis).
- `mode_diff_not_equal`; `uid_gid_diff_not_equal` (Q9: uid/gid INCLUDED);
  `uname_gname_diff_not_equal`; `xattr_diff_not_equal`;
  `symlink_target_diff_not_equal`; `whiteout_vs_file_not_equal`.
- `order_independence`: same entries fed in different tar order ⇒ equal digest.
- `scheme_version_byte`: bumping the version byte changes every digest.

ChainID: known-answer test against a hand-computed 3-layer chain; equal DiffID
prefixes ⇒ equal ChainID prefixes.

Codec (`codec_test.go`):
- `roundtrip`: LayerIndex → bytes → LayerIndex, deep-equal including Xattrs.
- `truncated_stream_detected`; `unknown_major_version_rejected`;
- `header_first_line`: header parseable without reading entries.

## Acceptance criteria

- `go test ./internal/...` green; every named case above exists and passes.
- `internal/domain` and `internal/analyze` import neither `net/http` nor any
  gcr/moby package (verifiable via `go list -deps`); `internal/index` adds only
  klauspost/compress.
- Indexing a generated 100k-entry in-memory tar completes in one pass with
  memory proportional to entry count, not content bytes (content is drained,
  never buffered — code-review-checkable: no `io.ReadAll` on file bodies).
- ARCHITECTURE §3.1's stale serialization snippet corrected; delta recorded in
  DECISIONS.md.

## Risks / gotchas

- **The §3.1 contradiction** (step 5) is the one place two approved documents
  disagree; Q9 wins. Do not "split the difference" — the §9.1 test list
  (uid/gid/uname/gname/xattr differences must change the digest) is the oracle.
- Whiteout paths: `.wh.` applies to the *basename*; `dir/.wh.x` targets
  `dir/x` — keep target-path computation out of display paths.
- zstd Reader must be `Close()`d (goroutine leak otherwise); use one shared
  decoder pool if profiling warrants, but correctness first.
- `archive/tar` returns `io.EOF` at end but also on some malformed archives —
  distinguish clean EOF from mid-entry truncation (declared DiffID check is the
  backstop).
- Keep `Entry` compact (~200 B budget per §4.6); don't add fields casually.
