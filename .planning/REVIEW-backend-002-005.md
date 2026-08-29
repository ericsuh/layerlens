# Review — Backend phases 002–005

Independent, report-only review of `e1f1865`, `c7744d1`, `7798886`, `0808aa5`.
Baseline: `go vet` clean, `golangci-lint run ./...` 0 issues, `go test -race`
passes, fixtures reproduce bit-for-bit, binary driven with curl against all five
fixture pairs. Findings marked **[repro]** were reproduced by running code;
**[read]** were found by inspection.

**Verdict: sound enough to build on.** The algorithmic core is correct —
squashing is genuinely order-independent, the tarsum-v1 field set matches Q9 on
both the digest and the diff predicate, aggregation is arithmetically consistent
across whole trees, and the honesty boundary between trunk and dotted edges holds
all the way to the wire.

## Major

- **M1 [repro] — one legal request can return a 311 MiB body.**
  `handlers_diff.go:162` embeds `min(limit, len(children))` grandchildren *per
  row* at `depth=2`, and both `depth=2` and `limit=1000` are accepted, so a
  1000×1000 directory yields 1,000,000 rows: **326,269,144 bytes, 1,133 MiB heap
  delta**, from a single request, concurrency-multiplied. `errors.go:452` uses
  `json.NewEncoder(...).Encode`, which materializes the whole value first. This
  breaks §4.6's 1.5 GiB RSS ceiling and contradicts §6.5's own ~70 KB claim —
  the spec sanctions a `limit × (1 + limit)` bound that cannot coexist with it.
  `TestTreeDefaultPageIsBounded` only measures the *default* page, so nothing
  catches it. **Fix before the frontend ships a control that can request it.**

- **M2 [repro] — `Squash` retains every layer index at once.**
  `comparison.go:647` loads all N indexes into a slice before squashing, so peak
  is Σ over layers rather than §4.6's budgeted per-layer transient. Measured
  **512 MiB peak** for 30 layers × 50k entries whose squashed tree holds only 50k
  paths; both sides sequentially puts a comparison near 1 GiB well below the
  500k-file target. This is the difference between the 25 GiB claim being
  structural and aspirational. **Fix:** apply-and-drop each index incrementally.

- **M3 [repro] — concurrent `PutLayer` of the same DiffID permanently
  double-charges the cache accounting.** `store.go:511` is check-then-act: two
  transactions that both miss will both `reserve`, both commit the same
  directory, and one map entry overwrites the other. Eviction later subtracts
  once, so the drift is monotonic: after evicting everything, `accounted=8072`
  with `disk=0`. A long-running server eventually returns `507 cache_full` on an
  empty disk. ARCHITECTURE §5 promises "a mutex per layer digest" — it does not
  exist. Unreachable today (fixtures ingest sequentially) but **normal once phase
  008 lands concurrent pulls.**

## Minor

- **m1 [repro]** — `ChangesetDigest` sorts by `Path` alone with an unstable sort,
  so it is order-*dependent* when a path holds both an object and its whiteout
  marker, contradicting its own doc comment. Masked in production because
  `indexState.finish` sorts by `(Path, Kind)` first; the existing test uses four
  distinct paths so it would pass even if this were live. Sort by `(Path, Kind)`.
- **m2 [repro]** — `dir/.wh..` resolves to a whiteout of the *parent directory*
  (`TrimPrefix` yields `"."`, `path.Join` normalizes it away), and aufs metadata
  like `.wh..wh.aufs` becomes phantom whiteouts that inflate `EntryCount` and
  pollute the changeset digest. Docker's `pkg/archive` skips the `.wh..wh.`
  prefix outright. Reject `.`/`..` and skip that prefix.
- **m3 [repro]** — `fieldsOfNode` zeroes `Size`/`ContentSHA` for directories but
  `fieldsOfEntry` does not, so the digest and the modified predicate diverge on
  directory size. §3.2 claims this is structurally impossible; it isn't. Errs
  safe (suppresses an edge) but the guarantee should be real.
- **m4 [repro]** — a dir↔file type change drops the vanished subtree's bytes from
  `LeftBytes`, so the row (and the root total) understates the left image.
  §6.5 documents that field as "subtree regular-file bytes per side". Either
  count them or document the exception so the UI can label it.
- **m5 [repro]** — synthetic implicit-directory metadata reaches the wire: an
  explicit `/d` (0700) against an implicit one yields `status: unchanged` with
  `right.mode=0755`, a value invented by `ensureDirs`. Carry `implicit` into
  `SideMeta` so the client can render "—" instead of a fabricated mode.
- **m6 [read]** — an evicted-layer 404 always names the *left* image
  (`handlers_diff.go:106`), so a client refetches the wrong one.
- **m7 [read]** — `Txn.Commit` validates with `HasLayer` rather than "declared by
  this transaction", so an undeclared layer can be evicted between check and
  rename, producing the corruption state the sweep exists to clean up.
- **m8 [read]** — `Ingest`'s already-present short-circuit never upgrades
  provenance, so an image first pulled from a registry and later loaded as a
  fixture stays unpinned — exactly the image an LRU sweep should not delete.
- **m9 [repro]** — `/api/v1/meta.allowedRegistries` is permanently `[]`; §6.6
  says the UI reads it to name valid pull targets.

## Nits

- `comparison.go:558`'s comment about contexts describes the handler, not the
  code; it is true only because `handleDiffTree` ignores the passed ctx. A caller
  trusting the comment would let one disconnecting client cancel every waiter.
- `parseLayerCount` accepts `+1`.

## Clean (verified, not assumed)

Path safety and input validation; cache atomicity, flock, sweep, LRU,
refcounting and the `cache_full` refusal (1600 concurrent reads during eviction
produced zero torn indexes); squash/whiteout/opaque order-independence; the Q9
field set on both digest and predicate; trunk LCP and could-be-shared degenerate
cases; aggregation (every subtree's 11-field aggregate independently recomputed
from leaf rows across three fixture pairs, zero mismatches); API conformance and
pagination invariants; the honesty requirement; fixture determinism; and test
quality — the tests measure allocator counters, reconstruct real crash states,
and inject clocks rather than sleeping.
