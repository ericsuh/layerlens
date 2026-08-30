# DECISIONS.md — layerlens technology decisions

All versions and facts below were verified on **2026-08-29** against proxy.golang.org, registry.npmjs.org,
api.github.com, raw spec files, or by compiling/running code in the dev sandbox (Linux arm64, Go 1.26.0,
Node v22.22.1, Docker Engine 29.7.2 with containerd image store). "Verified live" means I ran it here.

## Decisions at a glance

| Area | Choice | Version | Why (one line) |
|---|---|---|---|
| Registry image reads | `github.com/google/go-containerregistry` | v0.22.0 (2026-08-21) | One lib covers remote pull, auth token dance, platform selection, streaming layers, daemon, tarball/OCI layout; verified live in sandbox |
| Docker daemon reads | `docker save` stream via go-containerregistry `pkg/v1/daemon` (unbuffered) | v0.22.0 | Only supported API path; Engine 29 emits OCI-layout saves which gcr parses; stream once to disk, index, discard |
| Layer tar parsing / whiteouts | stdlib `archive/tar` + our own ~50-line whiteout logic | Go 1.26 stdlib | No library produces metadata-only merged trees; existing libs (moby/go-archive, continuity) extract to real filesystems |
| Layer identity for "shared trunk" | ChainID over uncompressed DiffIDs (OCI image-spec config.md) | spec v1.1.1 | ChainID is what determines local layer-store sharing; compressed digest only governs registry blob dedupe |
| "Could-be-shared" detection | Normalized changeset digest (sorted entries + per-file content sha256, timestamps excluded) | ours | DiffID includes tar mtimes/ordering, so identical content ≠ identical DiffID; must hash normalized content |
| Frontend framework | React + TanStack Query | react 19.2.8, @tanstack/react-query 5.102.8 | Mandated by spec; both current and active |
| UI components | shadcn/ui (Radix primitives) + Tailwind CSS v4 | shadcn CLI 4.19.0, tailwindcss 4.3.3 | Zero CSS-in-JS runtime, esbuild-friendly, components are vendored source we can restyle for diff hatching |
| Tree view | `@headless-tree/react` + `@tanstack/react-virtual` | 1.7.0 / 3.14.10 | Headless flat-list model = trivial virtualization + fully custom row rendering (diff colors, size bars); first-class a11y |
| Layer fork diagram | Hand-rolled SVG/CSS | — | Graph is a fixed 2-branch fork; xyflow/dagre are 100 kB+ of unneeded generality; dotted SVG paths are ~30 lines |
| Bundler | esbuild | 0.28.2 | Mandated; Tailwind runs as a separate CLI step, not an esbuild plugin |
| TypeScript | typescript | 5.9.3 (typecheck only) | esbuild transpiles; tsc 5.9.x keeps typescript-eslint 8.68.0 compat (TS 7.0.2 exists — see Open questions) |
| Go tests | `go test` + testify | testify v1.12.1 | Table-driven tests + assert/require; ubiquitous |
| Go lint | golangci-lint | v2.13.2 (2026-08-27) | Standard meta-linter; v2 config format |
| TS unit tests | Vitest + Testing Library | vitest 4.1.11, @testing-library/react 16.3.3 | esbuild-native transforms, jest-compatible API, no babel |
| E2E | Playwright `webServer` against the built Go binary | @playwright/test 1.62.1 | Runs the real server; fixtures are deterministic generated OCI layouts, no network |
| Toolchain/tasks | mise (`[tools]` + `[tasks.*]` with `depends`) | v2026.8.14 | Mandated; task dependency syntax verified against docs |
| Cache format | Per-layer metadata index (JSONL+zstd keyed by DiffID) — never keep extracted filesystems | — | ~15–30 MB per 500k-file layer set vs 25 GiB extracted |

---

## A. Go libraries for OCI/Docker images

### A1. Remote registry reads (no daemon) — **go-containerregistry v0.22.0**

Module: `github.com/google/go-containerregistry` — v0.22.0, released 2026-08-21 (proxy.golang.org).
Actively maintained by Google (used by crane, ko, kaniko, cosign).

**Verified live in the sandbox** (scratch program compiled and ran):

```go
ref, _ := name.ParseReference("alpine:3.20")
ref.Context().RegistryStr() // "index.docker.io" — the hook for the SSRF allowlist
img, _ := remote.Image(ref,
    remote.WithAuthFromKeychain(authn.DefaultKeychain),
    remote.WithPlatform(v1.Platform{OS: "linux", Architecture: "amd64"}))
cf, _ := img.ConfigFile()           // RootFS.DiffIDs, History
layers, _ := img.Layers()
l.Digest(); l.DiffID(); l.Size()    // compressed digest, uncompressed digest, compressed size
rc, _ := l.Uncompressed()           // io.ReadCloser — streams; never buffers the layer
```

Anonymous pull of `alpine:3.20` from Docker Hub worked from the sandbox, including the
token-auth dance and index→`linux/amd64` platform selection via `remote.WithPlatform`
(it resolves a manifest index to the matching child manifest, and correctly ignores
BuildKit attestation manifests with platform `unknown/unknown`).

- **Auth for the 5 registries**: the bearer-token challenge flow (`WWW-Authenticate` →
  token endpoint) is handled transparently and is what Docker Hub, GHCR, GCR/Artifact
  Registry, ACR, and ECR-Public all speak. `authn.DefaultKeychain` reads
  `~/.docker/config.json` (incl. credential helpers) and falls back to anonymous.
  For non-interactive cloud creds there are keychain adapters composable via
  `authn.NewMultiKeychain` (verified present in v0.22.0):
  - GCR: `pkg/v1/google.Keychain` (ships in the module)
  - ECR private: `github.com/awslabs/amazon-ecr-credential-helper/ecr-login` v0.12.0 (2026-02-25) via `authn.NewKeychainFromHelper`
  - ACR: `github.com/chrismellard/docker-credential-acr-env` (stale: last release 2023-03; only needed for AAD-token auth — ACR admin user/password works through DefaultKeychain)
  Note: **ECR private has no anonymous access** (ECR Public at `public.ecr.aws` does). ACR
  anonymous pull is a per-registry opt-in. See Open questions.
- **Streaming 25 GiB layers**: `Layer.Compressed()`/`Uncompressed()` return readers backed
  by the HTTP response body — nothing is buffered in memory. We stream each layer's tar
  exactly once, building the metadata index on the fly, and never write the blob to disk.

**Runner-up: `github.com/regclient/regclient` v0.11.5 (2026-05-26).** Solid, actively
maintained, excellent multi-registry support and manifest tooling (`regctl`). Rejected
because its API is registry-operations-oriented (copy/mod/sync); it lacks gcr's
`v1.Image`/`v1.Layer` abstraction that unifies remote, daemon, and on-disk sources, and
lacks the daemon integration we need anyway.

**Rejected:**
- `oras.land/oras-go/v2` v2.6.2 (2026-07-10): OCI artifact push/pull oriented; no platform-resolving image abstraction, no daemon support.
- `github.com/containerd/containerd/v2` v2.3.4: a client for a containerd *daemon* — wrong shape entirely for daemonless registry reads, huge dependency surface.

### A2. Local Docker daemon reads — `docker save` stream (gcr `daemon` package)

There is **no cheaper API path than `docker save`** for layer content: the Engine API only
exposes image filesystems via `GET /images/{name}/get` (= `docker save`). Reading
`/var/lib/docker` directly requires root and snapshotter-specific knowledge — rejected.

- `daemon.Image` in go-containerregistry calls `client.ImageSave` under the hood
  (verified in v0.22.0 source, `pkg/v1/daemon/image.go:61`). **Must use
  `daemon.WithUnbufferedOpener()`** — the default buffers the whole tarball in memory,
  which is fatal at 25 GiB. Even unbuffered, each `Layers()[i]` access re-opens the save
  stream, so for our one-pass indexing we should instead call `ImageSave` once ourselves
  (via `github.com/moby/moby/client`, which gcr already depends on), stream the tarball
  to the cache dir (or index it in-flight), and parse it with gcr's `tarball`/`layout` packages.
- **Verified live**: sandbox Docker Engine 29.7.2 runs the containerd image store
  (`driver-type io.containerd.snapshotter.v1`), and its `docker save` output is an **OCI
  layout tarball** (`oci-layout`, `index.json`, `blobs/sha256/...`) that also includes a
  legacy `manifest.json` for compatibility — both gcr parsers work.
- Gotcha: with the containerd store, a multi-platform image saves *all* platforms by
  default; pass `--platform linux/amd64` / the API equivalent when exporting.
- Cheap listing for the image-picker: `client.ImageList` + `ImageInspect` (no data transfer).
- Optimization worth keeping: if `ImageInspect` shows a `RepoDigests` entry for an
  allowlisted registry, prefer the registry path (A1) over a 25 GiB local save — the
  bytes are the same and the registry path lets us skip layers we've already indexed
  (by DiffID) without streaming them from the daemon.

### A3. Layer tar parsing + whiteouts — stdlib `archive/tar` + our own logic

Recommendation: **write it ourselves** on stdlib `archive/tar`. The whiteout rules are
small and we only want *metadata*, which no existing library targets:

- `github.com/moby/go-archive` v0.3.3 (2026-08-05, Apache-2.0): applies changesets by
  extracting onto a real filesystem (`ApplyLayer`) — we never extract, so it solves the
  wrong problem.
- `github.com/containerd/continuity` v0.5.0 (2026-04-29, Apache-2.0): filesystem
  manifests/comparison of on-disk trees; same mismatch.
- `github.com/wagoodman/dive` v0.13.1 (release 2025-03-29; repo pushed 2025-12-15; MIT;
  54k stars): its `dive/filetree` package *is* importable (module
  `github.com/wagoodman/dive`, package not under `internal/`) and does exactly this kind
  of metadata tree with whiteout handling. We should **read it as a reference but not
  import it**: its `FileNode`/`FileTree` model is built for its TUI (per-node view state,
  string-keyed child maps, tree-copy-per-layer) and would fight our server-side
  aggregation and serialization needs.

Whiteout rules to implement (OCI image-spec v1.1.1 `layer.md`, quoted below in Key
technical facts): `.wh.<basename>` deletes the named sibling from lower layers;
`.wh..wh..opq` in a directory hides *all* lower-layer children of that directory and is
applied **before** the layer's own entries in that directory regardless of tar order;
whiteouts apply only to lower layers; readers MUST accept both explicit and opaque forms.
Also handle: hardlinks (`Typeflag == TypeLink` — count size once), PAX/GNU long names
(stdlib handles), path normalization (`./`, leading `/`, `..` rejection), and duplicate
entries in one tar (last wins).

### A4. Layer identity: compressed digest vs DiffID vs ChainID — **the core correctness decision**

Three different identities, three different jobs (citations in Key technical facts):

1. **Compressed digest** (`layers[].digest` in the *manifest*): identity of the compressed
   blob in the registry. Governs *registry-side* storage/pull dedupe (blobs are shared
   per-repository content-addressably; cross-repo mounts use it).
2. **DiffID** (`rootfs.diff_ids[]` in the *config*): digest of the *uncompressed* layer
   tar. Independent of compression settings — the same DiffID can appear under different
   compressed digests (gzip level, zstd, etc.).
3. **ChainID**: `ChainID(L0)=DiffID(L0)`; `ChainID(L0..Ln)=Digest(ChainID(L0..Ln-1) + " " + DiffID(Ln))`.
   Identifies a layer *in the context of everything below it*. This is what the local
   layer store (Docker graphdriver and containerd snapshotter keys) uses to share
   extracted layers between images.

**Decision:** the shared trunk is computed by **longest common prefix of the
`rootfs.diff_ids` arrays** — which is by construction the longest common ChainID chain.
Two images "share a layer cache" for layers 1..k iff their first k DiffIDs are pairwise
equal *in order*. An identical DiffID at *different* positions (or above a differing
layer) is NOT shared storage — that is exactly our dotted-line "could-be-shared" case.
We display both the compressed digest (what you see in `docker pull` output / manifests)
and the DiffID per layer, but all trunk/sharing logic keys on DiffID/ChainID.

For pull-bandwidth sharing specifically (registry blob dedupe) the compressed digest is
the true key; the UI copy should say "shared layer cache" (ChainID semantics) as the spec
requires, and can note when compressed digests also match.

**"Could-be-shared"**: DiffID equality is *sufficient* but not *necessary* for "same
changes" — tar mtimes, entry order, uid/gid and padding all feed the DiffID, so two
builds that produced identical file content usually have different DiffIDs. We therefore
compute a **normalized changeset digest** per layer: sort entries by path; hash
(path, typeflag, mode, uid, gid, symlink target, content-sha256, whiteout markers);
exclude timestamps. Two post-fork layers with equal normalized digests get the dotted
edge. This requires hashing every regular file's content during the single indexing pass
(SHA-256 while streaming; CPU-bound but one-pass). Whether mode/uid/gid participate is a
knob — see Open questions.

> **RESOLVED 2026-08-29, then REVISED same day** (user decisions, `RESEARCH.md` Q5 then
> **Q9**). **Current rule: tarsum-v1 field selection — content + mode + uid/gid +
> typeflag + linkname + size + devmajor/devminor + sorted xattrs, excluding mtime.**
> This is literally the field set Docker/BuildKit uses for its build cache, verified
> against `moby/buildkit` `cache/contenthash/{filehash,tarsum}.go`: `v1TarHeaderSelect`
> takes the v0 list and copies it *"excluding the 'mtime' header"*. The delta from the
> first resolution is that **uid/gid are now included** — we follow Docker rather than
> our own judgment call. The superseded first answer was:
> **content + mode bits; mtime and uid/gid excluded**. The normalized changeset digest
> hashes sorted entries of `(path, typeflag, mode bits, symlink/link target,
> content-sha256, whiteout marker)`. Excluding mtime is mandatory — it is the very thing
> that makes two byte-identical rebuilds produce different DiffIDs, which is the
> phenomenon this tool exists to expose — and Docker's build cache excludes it too.
> Including mode bits is mandatory — two layers are not interchangeable if one ships a
> non-executable binary.
>
> This makes the two features map onto Docker's two real caches: the **shared trunk** is
> layer-store semantics (ChainID over DiffIDs, byte-exact, mtime included), and the
> **could-be-shared edges** are build-cache semantics (tarsum v1, mtime excluded). The
> sketch in the paragraph above is therefore correct as written.

### A5. Dockerfile instruction recovery — config `history` with `empty_layer` offset

From OCI image-spec v1.1.1 `config.md` (verified): `history` is ordered oldest-first;
`history[i].created_by` is "the command which created the layer"; `empty_layer` "is set
to true if this history item doesn't correspond to an actual layer in the rootfs section".

**Mapping algorithm:** walk `history` in order keeping a cursor into `diff_ids`; entries
with `empty_layer: true` (ENV, LABEL, CMD, WORKDIR, EXPOSE…) consume no diff_id; every
other entry consumes the next diff_id. If the counts of non-empty history entries and
diff_ids disagree (hand-built or squashed images), fall back to "instruction unknown" per
layer rather than misaligning — never trust the mapping blindly.

BuildKit quirks (all common in modern images):
- `created_by` is the raw Dockerfile instruction suffixed with `# buildkit`
  (e.g. `RUN /bin/sh -c npm install # buildkit`), and `comment: buildkit.dockerfile.v0`.
  Classic builder instead writes `/bin/sh -c #(nop) COPY ...` forms. Strip both
  decorations for display; keep raw text in the tooltip.
- `COPY --link` and cache mounts produce `created_by` that doesn't match the literal
  Dockerfile line; heredoc RUNs embed the whole script.
- Buildx-pushed images may carry **attestation manifests** in the index with platform
  `unknown/unknown`; `remote.WithPlatform` already skips them, but any hand-rolled index
  walking must too.
- Base-image history is inherited verbatim, so the trunk layers show the base image's own
  instructions — desirable for us.

## B. Prior art

- **wagoodman/dive** (MIT, v0.13.1, repo active as of 2025-12): builds a `FileTree` per
  layer from tar headers, then produces cumulative views by stacking trees and applying
  whiteouts; "efficiency" is computed by finding paths that appear in multiple layers
  (wasted bytes = re-added/overwritten/deleted file sizes). Single-image only, TUI only.
  Lessons taken: (1) tar-header-only metadata is enough for everything except
  content-equality — which is why we add per-file content hashes; (2) its per-layer
  tree-copy approach is memory-hungry — we keep flat sorted entry lists per layer and
  merge on demand; (3) its `RefTrees`/`StackedTree` split maps cleanly onto our
  "cumulative filesystem at layer k" requirement.
- **GoogleContainerTools/container-diff**: **archived** (repo archived; last push
  2024-03-27, last tag v0.19.0 2024-02-21). Did two-image diffs of files/packages but
  flat lists only, no layer-level analysis, no whiteout-aware cumulative-at-layer-k
  views. Confirms the niche is real; not reusable.
- **reproducible-containers/diffoci** v0.1.8 (2026-02-04): byte-level diff for
  reproducibility verification (CLI); no cumulative/layer-point selection, no UI. Its
  existence validates the normalized-comparison idea (it ignores timestamps optionally).
- **skopeo / regclient**: registry-object tools (copy, inspect, manifest diff); no
  filesystem-level diffing.
- Nothing found that does the fork-trunk visualization or could-be-shared detection —
  that part is greenfield.

## C. Frontend

### C1. Framework: React 19.2.8 + @tanstack/react-query 5.102.8 (both verified 2026-08)

Query v5 with `useQuery`/`useInfiniteQuery` fits the "server aggregates, client pages"
model; ephemeral UI state stays in React state — no Redux/Zustand needed.

### C2. Components: **shadcn/ui + Radix + Tailwind v4** (shadcn CLI 4.19.0, tailwindcss 4.3.3)

Requirements recap: great tree story, tables/buttons/tooltips/popovers, a11y, esbuild-
compatible, no CSS-in-JS runtime, sane bundle.

- **shadcn/ui (chosen)**: components are vendored TypeScript source (Radix primitives +
  Tailwind classes) — no runtime styling system, tree-shakes perfectly under esbuild, and
  we can surgically restyle rows for diff coloring/hatching (CSS `repeating-linear-gradient`
  hatch fills). Tooltip/Popover come from Radix (`@radix-ui/react-tooltip` 1.2.16,
  `@radix-ui/react-popover` 1.1.23, verified). shadcn has no tree component — supplied
  separately (C3), which every candidate here would have needed anyway.
- **Runner-up — Mantine 9.5.2** (verified: CSS-modules based, zero CSS-in-JS runtime deps;
  peer react ^19.2): batteries-included and esbuild-safe; its built-in `Tree` is not
  virtualized, and adopting its full theming system is heavier than we need next to Tailwind.
- **Rejected — Chakra v3 (3.37.0)**: still requires the Emotion runtime (verified peer dep
  `@emotion/react >=11`) — exactly the CSS-in-JS runtime we're avoiding.
- **Rejected — Ant Design 6.6.2**: `@ant-design/cssinjs` runtime (verified dep), large
  bundle, hard to restyle for hatched diff rows.
- **Rejected — Headless UI 2.2.10**: too small a primitive set (no tooltip); Radix-via-shadcn dominates it.
- **Rejected — Base UI (@base-ui-components/react 1.0.0-rc.0)**: still RC as of 2026-07; promising but not worth pre-1.0 churn.

### C3. Tree + virtualization: **@headless-tree/react 1.7.0 + @tanstack/react-virtual 3.14.10**

- `@headless-tree` (MIT, repo pushed 2026-07-20; the official successor to
  react-complex-tree, per its README): exposes the tree as a **flat list of visible
  items** with full a11y (ARIA tree emulation), keyboard nav, and search — and explicitly
  supports plugging any virtualizer over the flat list. That gives us: 100k+ entry
  virtualization via TanStack Virtual, completely custom row rendering (indent guides,
  disclosure triangles, add/remove coloring, per-row size bars), and async per-folder
  loading that matches our server-side pagination/aggregation API. Drill-down ("open
  folder as new root") is just re-rooting the data source.
- **Runner-up — react-arborist 3.16.0** (MIT, pushed 2026-07-25): built-in virtualization
  and polished, but designed around a fully-loaded client-side tree; lazy per-folder
  server loading is bolted-on, and we'd fight it at 500k nodes.
- **Rejected — react-window 2.3.0**: list virtualizer only (fine, but TanStack Virtual
  integrates more cleanly with headless-tree's flat list and dynamic row counts).
- TanStack Virtual is also reused for any long flat tables (layer file lists).

### C4. Fork-and-branch layer diagram: **hand-rolled SVG/CSS**

The graph is structurally fixed: one trunk column, one fork point, two branch columns,
plus optional dotted "could-be-shared" arcs between branches. Layout is trivial
(y = layer index; x = branch). A `<svg>` overlay for the fork elbow and dotted bezier
edges (`stroke-dasharray`) over CSS-grid-positioned layer cards is ~50 lines and keeps
layer cards as normal DOM (tooltips, selection, focus). 

**Runner-up — @xyflow/react 12.11.5** if the diagram ever needs pan/zoom/drag or n-way
comparison; dagre 3.1.1 / elkjs 0.12.0 are auto-layout engines we simply don't need for
a 2-branch tree. Rejected now: 100 kB+ and a custom-node abstraction tax for zero layout
problems solved.

### C5. Build: esbuild 0.28.2 + TypeScript 5.9.3 + Tailwind v4 CLI

- esbuild bundles/minifies TS+TSX directly; `tsc --noEmit` (5.9.3) is the typecheck gate.
  TS 7.0.2 (the Go-native compiler) is now `latest` on npm; staying on 5.9.3 keeps
  typescript-eslint 8.68.0 compatibility guarantees — revisit post-MVP (Open questions).
- **Tailwind v4 has no esbuild plugin path worth taking**: the current first-party
  integrations are `@tailwindcss/cli` 4.3.3, `@tailwindcss/postcss` 4.3.3, and a Vite
  plugin. Decision: run `@tailwindcss/cli -i src/app.css -o dist/app.css` as its own mise
  task parallel to esbuild (v4 configures via CSS `@theme`, no tailwind.config.js;
  content scanning is automatic). The third-party `esbuild-plugin-tailwindcss` (2.2.0)
  exists but adds a nonstandard layer for no gain over two watch processes.
- Output embeds via `//go:embed dist` in the Go server; esbuild's `--metafile` guards bundle size.
- eslint 10.9.1 + typescript-eslint 8.68.0 for linting.

## D. Testing / toolchain

- **Go**: `go test ./...`; `github.com/stretchr/testify` v1.12.1 (2026-08-17) for
  assert/require in table-driven tests; **golangci-lint v2.13.2** (2026-08-27, v2 config
  schema) run via mise. `go vet` is included in golangci-lint's defaults.
- **TS unit**: **Vitest 4.1.11** — esbuild-based transforms out of the box (no babel/ts-jest),
  Jest-compatible API, first-class ESM; jsdom 30.0.1 (or happy-dom 20.12.0) environment +
  `@testing-library/react` 16.3.3. Jest rejected: needs a transformer bolt-on for TS/ESM
  and duplicates our toolchain.
- **Playwright 1.62.1** (verified): `playwright.config.ts` `webServer: { command: "mise run
  build && ./bin/layerlens --listen :43117 --data-dir .e2e-data", url: "http://localhost:43117/healthz",
  reuseExistingServer: !process.env.CI }` — e2e always exercises the real embedded-SPA binary.
  **Deterministic fixtures, no network**: a small Go fixture generator (`go run
  ./cmd/genfixtures`) synthesizes `example:v1`/`example:v2` as OCI image layouts on disk
  (fixed timestamps, handcrafted layers with shared trunk, a could-be-shared pair, and
  whiteouts) into the server's image directory; the server's "prior analyzed images" path
  loads them. This keeps Mac e2e runs Docker-free and byte-stable. (Live registry pull
  gets one separately-tagged, skippable smoke test.)
- **mise v2026.8.14** (2026-08-26): syntax verified against docs — `[tools]` pins
  (`go = "1.26"`, `node = "22"`, `golangci-lint = "2.13.2"`), `[tasks.<name>]` with `run`
  (string or list, run serially), `env`, `dir`, `sources`/`outputs` for caching, and
  **`depends = ["a", "b"]` — task dependencies are supported** (deps run first, in
  parallel where possible). Example: `[tasks.deploy] depends = ["build-linux"] run = "./scripts/deploy.sh"`.

## Key technical facts (with citations)

**DiffID / ChainID / manifest digest** — OCI Image Spec v1.1.1, `config.md`
(https://github.com/opencontainers/image-spec/blob/v1.1.1/config.md), verbatim:
- DiffID: "A layer DiffID is the digest over the layer's uncompressed tar archive…"
- `rootfs.diff_ids`: "An array of layer content hashes (DiffIDs), in order from first to last."
- ChainID: "While a layer's DiffID identifies a single changeset, the ChainID identifies
  the subsequent application of those changesets." Formula:
  `ChainID(L0)=DiffID(L0)`; `ChainID(L0|…|Ln) = Digest(ChainID(L0|…|Ln-1) + " " + DiffID(Ln))`.
- Manifest `layers[].digest` (https://github.com/opencontainers/image-spec/blob/v1.1.1/manifest.md)
  addresses the (usually compressed) blob as stored in the registry.
- ⇒ Local layer-cache sharing ≙ ChainID equality ≙ equal DiffID *prefix*; registry
  blob/pull dedupe ≙ compressed digest equality.

**Whiteouts** — OCI Image Spec v1.1.1, `layer.md`
(https://github.com/opencontainers/image-spec/blob/v1.1.1/layer.md), verbatim:
- "A whiteout filename consists of the prefix `.wh.` plus the basename of the path to be deleted."
- "Whiteout files MUST only apply to resources in lower/parent layers."
- Opaque whiteout `.wh..wh..opq` "hides all children" of its directory, and is "applied
  first, before creating the new version" of siblings in the same layer, "regardless of
  the ordering in which the whiteout file was encountered."
- Readers "MUST accept both" explicit and opaque whiteouts.

**History mapping** — `config.md` ibid.: `history[].empty_layer` "is set to true if this
history item doesn't correspond to an actual layer in the rootfs section"; non-empty
entries map positionally onto `diff_ids`.

**Docker Engine 29 + containerd store** (verified live in sandbox): `docker save`
produces an OCI layout tarball with compat `manifest.json`; multi-platform images need
explicit platform selection at save time.

## Implementation deltas

Recorded per the PROJECT.md workflow rule: anything implementation proved wrong
or under-specified in an earlier planning file, with the source file updated.

### Phase 001 (toolchain & walking skeleton), 2026-08-29

- **npm versions that did not exist / did not resolve** (the rest of §C/§D
  installed exactly as pinned — react 19.2.8, react-dom 19.2.8,
  @tanstack/react-query 5.102.8, typescript 5.9.3, esbuild 0.28.2,
  tailwindcss + @tailwindcss/cli 4.3.3, vitest 4.1.11, jsdom 30.0.1,
  @testing-library/react 16.3.3, eslint 10.9.1, typescript-eslint 8.68.0,
  @playwright/test not yet installed — phase 007):
  - `@eslint/js` has its own version line; there is no 10.9.1. Installed
    **@eslint/js 10.0.1** (latest), which is the companion package for
    eslint 10.9.1.
  - `eslint-plugin-react-hooks` (never pinned in DECISIONS) 7.0.1 caps its
    eslint peer at ^9; **7.1.1** is the first release that allows eslint ^10.
  - Added two packages DECISIONS did not name but the pinned ones require or
    imply: **@testing-library/dom 10.4.1** (peer of @testing-library/react 16)
    and **@types/react 19.2.8 / @types/react-dom 19.2.4**.
- **mise-resolved toolchain**: `go = "1.26"` → go1.26.7, `node = "22"` →
  v22.23.2, `golangci-lint = "2.13.2"` exact. All three installed from a clean
  cache in the sandbox, so `mise install` needs no manual fallback.
- **ARCHITECTURE §1.3 — new `--ui-dir` flag** (file updated). `mise run dev`
  runs the esbuild/Tailwind watchers next to `go run`, but `//go:embed` snapshots
  `internal/webui/dist` at compile time, so watcher output was invisible to the
  running server. `--ui-dir <dir>` (empty by default, warned about at startup)
  serves the asset directory from disk instead of the embedded FS. Development
  only; production and the systemd unit never set it.
- **ARCHITECTURE §6.1 — new `not_found` error code** (file updated). The table
  had no code for "unrouted path inside `/api`"; `image_not_found` would be a
  lie and falling through to the SPA shell would hand clients HTML. Unmatched
  `/api/*` now returns 404 `not_found` in the standard envelope.
- **`go test ./...` / `golangci-lint run` traverse `web/node_modules`**: npm
  dependencies ship Go sources (`flatted/golang/pkg/flatted`), which land inside
  the module's package space. The `mise` tasks are therefore scoped to
  `./cmd/... ./internal/...`, and `.golangci.yml` excludes `web/node_modules`
  so a bare `golangci-lint run` is clean too.
- **Tailwind v4 source scanning is explicit**: automatic detection walks up to
  the git root and would emit utilities for `.planning/prototype/*`, so
  `web/src/app.css` uses `@import "tailwindcss" source(none)` + `@source "../src"`.
- **SPA bundle format is IIFE, not ESM**: nothing in the bundle needs module
  scope or code splitting, and an IIFE + `<script defer>` is executable in
  environments (jsdom-based checks) that do not run `<script type="module">`.
  Production builds omit the source map so the 1 MB map is not embedded in the
  binary; `mise run dev` keeps it.

### Phase 001 review fixes, 2026-08-29

Applied from `REVIEW-phase-001.md` after phase 002 landed.

- **`mise run dev` no longer writes `internal/webui/dist`** (review M1/M2/M3,
  one root cause). `dist` is a build-only artifact that `go:embed` compiles in;
  letting the dev watchers write it made `build-web`'s `outputs` look fresh, so
  `mise run build` skipped the production bundle and shipped the unminified dev
  one plus its 1.8 MB source map (binary 8.63 MB → 11.45 MB, `GET /app.js.map`
  → 200). `dev` now builds into a gitignored `.dev-dist/` and runs the server
  with `--ui-dir .dev-dist`. The `dev` trap is installed before the watchers are
  backgrounded and kills saved PIDs instead of `kill 0`, which was signalling
  mise's own process group and orphaning a watcher. `test-go` gained
  `depends = ["build-web"]` so it no longer compiles the embed while
  `build-web` is deleting and rewriting it.
- **ARCHITECTURE §6.1 — new `method_not_allowed` error code** (file updated).
  405 responses were returning `bad_request`, which §6.1 pins to HTTP 400. The
  new row covers "the path exists but not for this method"; the response also
  carries `Allow`.
- **ARCHITECTURE §1.3 — `--docker-host` no longer defaults to the live
  `DOCKER_HOST`.** The flag default is printed by `-h`, and `DOCKER_HOST` can
  carry a hostname and credentials, so the flag defaults to empty and the
  environment is consulted after `fs.Parse`. The §1.3 `/var/run/docker.sock`
  autodetect (previously deferred without a delta) is now implemented: the path
  is used only when it is actually a socket.
- **DESIGN §2.1 — the size example `1.02 GiB` contradicted its own rule**
  (file updated). The stated rule is "one decimal below 100, none at ≥100", so
  the example is now `1.0 GiB`. The rule was also silent about the carry: the
  implementation rounds first, so 1,048,575 B renders `1.0 MiB` rather than
  `1024 KiB`. `web/src/lib/format.ts` had a dead branch
  (`value < 10 ? 1 : value < 100 ? 1 : 0`) that reduces to `value < 100 ? 1 : 0`.
  Phase 007 must extend this formatter, not fork it.
- **`web/go.mod` replaces the manual `./cmd/... ./internal/...` scoping.** A
  nested one-line module makes `web/` a boundary that `go test ./...` and
  `golangci-lint run` stop at, so the Go sources vendored inside
  `web/node_modules` are never compiled or linted. The mise tasks and the
  `.golangci.yml` `web/node_modules` path exclusion were simplified away.
- **New devDependency `@types/node` 22.20.1** (matching the pinned `node = "22"`).
  `web/src/bundle.test.ts` reads the built bundle off disk and evaluates it in
  jsdom, which needs the Node type declarations.
- **Only `--docker-host` reads the environment.** ARCHITECTURE §1.3 heads its
  list "Flags/env" but names an environment variable for exactly one flag.
  That is the implemented reading: `DOCKER_HOST` is consulted, nothing else has
  an env fallback. Recording it here because the review flagged it as deferred
  without a delta; adding `LAYERLENS_*` fallbacks for the remaining flags would
  be a new decision, not a fix.
- **Content-Security-Policy on the SPA shell.** layerlens is entirely
  self-hosted, so the policy is `default-src 'none'` with `script-src 'self'`,
  `style-src 'self'`, `img-src 'self' data:`, `font-src 'self'`,
  `connect-src 'self'` and `base-uri`/`form-action`/`frame-ancestors` set to
  `'none'`. Note for phase 007: `style-src 'self'` also forbids `style="…"`
  attributes. If the virtualized tree needs inline transforms, add
  `style-src-attr 'unsafe-inline'` rather than loosening `style-src`.
  *(Superseded by the Phase 006 delta below: that addition was made in phase
  006, for Radix's portalled overlays and the measured layer diagram.)*

### Phase 002 (domain model & streaming layer indexer), 2026-08-29

- **The `ARCHITECTURE.md` §3.1 contradiction was already resolved.** Phase 002's
  file (step 5) expected to find a stale
  `(Path, Kind, ContentSHA, Mode, LinkTarget)` serialization snippet excluding
  uid/gid and to have to correct it. Commit `6b6d884` had already rewritten
  §3.1 to the binding RESEARCH **Q9** tarsum-v1 field set, so there was nothing
  to fix. The implementation follows Q9:
  `(Path, Kind/typeflag, Mode&0o7777, UID, GID, Uname, Gname, Size, LinkTarget,
  Devmajor, Devminor, sorted Xattrs, ContentSHA)`, **mtime the only exclusion**,
  behind a scheme-version byte, with every field length- or width-prefixed. The
  superseded-Q5 wording that survives at DECISIONS §A4 and RESEARCH Q5 is
  explicitly labeled as historical and was left as the record of the reversal.
- **ARCHITECTURE §2 — `analyze` imports klauspost/zstd** (file updated). §2 said
  `analyze` imports `domain` only, but §4.1 puts media-type decompression
  (gzip/zstd/none) inside `IndexLayer`, which lives in `analyze`. Verified with
  `go list -deps`: `domain` is stdlib-only; `analyze` and `index` add
  klauspost/compress and nothing else; none of the three pull in `net/http`,
  go-containerregistry or moby.
- **ARCHITECTURE §5 — the index header carries three more fields** (file
  updated): `changesetDigest`, `contentBytes` and `warnings` join
  `{"v","diffId","entryCount"}`. `index` may not import `analyze`, so without
  them a decoded `LayerIndex` could not reproduce its own changeset digest and
  the codec would be lossy. `layer.json` is unchanged and still exists as the
  cheap sidecar summary.
- **ARCHITECTURE §7.3 — the archive-root member is skipped silently** (file
  updated). `sanitizePath` rejects `.`/`/`/`./` as specified, but GNU tar emits
  a root member in most real layer tars; treating it as a sanitization failure
  would have put a spurious warning on nearly every layer. The indexer
  recognizes it as the root and skips it without a warning; hostile names still
  warn.
- **`klauspost/compress` pinned at v1.19.2.** DECISIONS named the library
  (§Risks, cache format) but never pinned a version; this is the version
  `go get` resolved. No other pinned version changed.
- **Indexer uses one reusable copy buffer.** `io.Copy` allocates a fresh 32 KiB
  buffer per call, which on a 500k-file layer is gigabytes of garbage; the
  indexer holds a single 128 KiB scratch buffer and calls `io.CopyBuffer`
  through a wrapper that hides `tar.Reader.WriteTo` (which would otherwise
  bypass the buffer). A regression test streams a 1 GiB synthetic layer and
  asserts total allocations stay under 8 MiB.

### Phase 003 (analysis algorithms: squash, diff, aggregate, trunk, edges), 2026-08-29

- **Phase 002's indexer collapsed a path's object and its whiteout/opaque
  marker into one entry; fixed** (ARCHITECTURE §3.1 `LayerIndex.Entries` and
  §4.2's duplicate-paths bullet updated). `indexState.put` keyed everything by
  path, so the standard overlay representation of an opaque directory — the
  `var/cache/` member *plus* a `var/cache/.wh..wh..opq` member — lost whichever
  came first in tar order. In practice the directory's own mode/uid/gid was
  dropped and the squashed tree showed a synthetic 0755 implicit directory
  instead, and §4.2's pinned semantics ("opaque in a directory the layer also
  re-creates", "whiteout x while the layer also ships x") were unreachable:
  the two-pass squasher can only apply what the changeset still contains.
  Markers now live in their own keyspace (`{path, kind}`), and `Entries` is
  ordered by `(Path, Kind)` so the one path that can hold two entries still has
  a total, deterministic order for the changeset digest. Duplicate *filesystem*
  entries still resolve last-in-tar-wins. `TestIndexLayerWhiteouts/
  opaque_entry_captured` was updated to assert both entries survive.
- **ARCHITECTURE §4.3 — the directory "own meta changed" predicate is the whole
  tarsum-v1 field set, not `Mode` alone** (file updated). §4.3's pseudocode
  comment said "Mode only", which would have hidden a `chown` of a directory
  whose contents did not otherwise change — a real difference that the
  changeset digest *does* see, so the tree and the dotted edges would have
  disagreed about what "same" means, which §3.2 forbids. Directories are
  instead projected with `Size` and `ContentSHA` forced to zero (a directory
  has neither, and a tar directory member's `size` field is meaningless), so a
  single predicate serves files and directories. The `Implicit` exemption is
  unchanged and now covers all synthetic metadata, not just the mode; a *kind*
  change is still reported even when one side is implicit.
- **One field-set definition in code** (acceptance criterion made structural).
  `internal/analyze/fields.go` holds `tarsumFields` plus `fieldsOfEntry`,
  `fieldsOfNode`, `equal` and `writeFields`; `ChangesetDigest` hashes it and
  `Diff`'s `metaDiffers` compares it. The two cannot drift apart, and
  `TestDiffModificationPredicate` asserts on every case that the tree verdict
  and the digest equality agree.
- **A path that is a directory on one side and a non-directory on the other is
  a leaf in the diff** (ARCHITECTURE §4.3 as written, recorded here because it
  is a visible product consequence): the vanished subtree's bytes are not
  counted anywhere in the aggregates. Keeping it a leaf is what makes
  "directory Agg == Σ children" an exact invariant, which the tree API's size
  bars depend on; showing the lost subtree as removed rows would be more
  informative but would give directories their own byte contribution.
- **`CouldBeSharedEdge` lives in `analyze`, not `domain`**, and carries the
  matched `ChangesetDigest` alongside `leftIndex`/`rightIndex`/`diffIdEqual`
  (§6.4's wire shape is unchanged; the extra field is for diagnostics and the
  server DTO simply omits it).

### Phase 004 (fixture generator & vendored demo images), 2026-08-29

- **ARCHITECTURE §9.2 — the apt/ffmpeg layer is in *both* example images, not
  "in v2 only"** (file updated). The two images are two builds of the *same*
  Dockerfile, so they necessarily have the same number of steps; a v2-only
  final layer would have been a different Dockerfile. `.planning/DESIGN.md` §11
  and the approved prototype screenshot already showed the symmetric shape with
  two dotted edges, and `IMPLEMENTATION-phase-004.md` says "matching apt/ffmpeg
  layers", so §9.2's line was the outlier. Consequence worth stating: at the
  golden workflow's layer-8-vs-layer-8 comparison the whiteouts are applied on
  *both* sides, so the removed rows the demo shows there come from the COPY
  layer (`src/old-util.js`, `src/legacy/`); the whiteout deletions surface when
  the left selection is the pre-cleanup layer. Both are asserted in
  `TestExampleFfmpegWhiteoutsDelete` / `TestExampleCopyLayerDiff`.
- **The ffmpeg layers are byte-identical, so the demo carries both edge
  flavours** (ARCHITECTURE §9.2 updated). `npm install` writes files at the
  build clock, so its two layers differ only by mtime → `diffIDEqual: false`,
  the crux of the lesson. Files unpacked from `.deb` archives keep the
  archives' own timestamps, so the ffmpeg layer reproduces exactly →
  `diffIDEqual: true`. That is realistic *and* it means the UI's two edge
  states are both reachable from the demo data instead of only one.
- **`WORKDIR /app` is a real layer, not an `empty_layer` history entry**
  (ARCHITECTURE §4.0 lists WORKDIR among the empty-layer instructions; both
  happen in practice — the classic builder records it empty, BuildKit
  materializes the directory). The fixtures use the BuildKit behaviour, which
  is what DESIGN §11 and the prototype show (five trunk layers, the fifth
  `0 B · empty`). The empty-layer path is still exercised: each example config
  carries four `empty_layer` entries (`CMD ["bash"]`, two `ENV`s, the final
  `CMD`), asserted by `TestExampleHistoryMapping`.
- **Fixture *shape* is realistic, absolute sizes are ~1/20 scale.** File bodies
  are a short seeded ASCII banner followed by NUL padding: unique per path (so
  `ContentSHA` differences are real) and ~1000:1 compressible (so the committed
  tree is 227 KiB for ~180 MiB of nominal image content). ARCHITECTURE §10.8
  already accepts toy scale; recorded here because the numbers the UI displays
  (a 14 MiB `node` binary, a 4 MiB `lodash.js`, a 4.4 MiB `.git` pack) are
  deliberately a fifth to a twentieth of the prototype's, chosen to keep
  proportions and size-bar behaviour intact.
- **One OCI layout per pair, tags via `org.opencontainers.image.ref.name`**
  (ARCHITECTURE §9.2 updated with the convention). Phase 005's fixture loader
  should scan `--fixtures-dir` for subdirectories containing an `oci-layout`
  file, read each `index.json`, and take the display reference from that
  annotation. Blob sharing inside a layout is automatic (content addressing),
  which is why the example pair costs 144 KiB for two 8-layer images.
- **`cmd/genfixtures` writes the OCI layout by hand; go-containerregistry is a
  test-only dependency.** Hand-writing the manifest/config/index JSON is ~200
  lines and gives byte-level control over field order and formatting, which is
  what makes "regenerate, no git diff" hold across library upgrades. gcr
  (`layout.ImageIndexFromPath` + `validate.Image`) is then an *independent*
  checker of the result rather than both producer and validator. It is added to
  `go.mod` now because phase 006's ingest needs it anyway.
- **`fixtures/.gitattributes` is generated, not hand-written.** It marks
  `**/blobs/sha256/**` binary so git neither diffs a gzip stream nor rewrites
  line endings in one; keeping it an output of the generator means it cannot
  drift from the layout the generator actually writes.
- **The generator refuses duplicate tar members.** A repeated path inside one
  layer is legal in tar (last-in wins) and would build silently, but in a
  hand-written fixture it is always a mistake — and one that would quietly move
  what the property tests assert against. `buildLayer` errors instead
  (`TestDuplicateMembersAreRejected`).

### Phase 005 (cache store, fixture ingestion & analysis API), 2026-08-29

- **ARCHITECTURE §6.5 — the seven `TreeAgg` change breakdowns are optional on
  the wire** (file updated; TS fields are now `addedBytes?` etc., absent means
  zero). `leftBytes`/`rightBytes`/`leftFiles`/`rightFiles` are always present
  because every row renders them. The four absolute plus eleven total fields
  emitted unconditionally made a row ~418 B of JSON, so the default page
  (`limit=200, depth=1`) over the `wide` fixture was 84 KB — over §6.5's own
  "~70 KB" bound and over its "a row is ~250–350 bytes" estimate. In an
  unchanged subtree, which is most rows in any real image, those seven fields
  are ~130 B of `":0,"`. Omitting them brings the row to ~288 B and the default
  page to 58 KB, measured by `TestTreeDefaultPageIsBounded`. Phase 007 must
  therefore treat them as optional numbers defaulting to 0.
- **ARCHITECTURE §6.5 — a well-formed `path` that names nothing in the
  comparison is `bad_request`, not 404** (file updated). §6.5 listed only
  `400 bad_request | 404 image_not_found`; both images exist in this case, so
  `image_not_found` would be a lie, and inventing a `path_not_found` code for a
  parameter validation failure buys nothing. 404 stays reserved for an unknown
  or evicted image id.
- **ARCHITECTURE §1.2/§1.3 — fixture *discovery* is synchronous, fixture
  *analysis* is not** (file updated). Loading fixtures before binding the port
  would delay startup by the whole cold-cache ingest; loading them entirely in
  the background would make `/healthz` racy for a deployment that has no
  fixtures at all. Discovery (a `stat` per subdirectory) therefore runs before
  the listener opens and settles readiness immediately when there is nothing to
  load; the analysis runs in a goroutine and `/healthz` answers
  `503 loading` until it completes. A missing or empty `--fixtures-dir` is a
  warning: the fixtures are a demo convenience, and refusing to start without
  them would make the binary useless anywhere they are not deployed. A fixture
  load that *fails* also marks the server ready — an operator needs the API and
  the error message more than they need a process that refuses to answer.
- **ARCHITECTURE §5 — the cap check runs after the staged index is written**
  (file updated). §5 already says the check is enforced during ingest because
  index size is unknowable up front; the consequence worth writing down is that
  the accounting can transiently exceed the cap by at most one layer index. The
  *refusal* test still runs before any eviction, which is what makes RESEARCH
  Q7's "refuse rather than thrash-evict" structural rather than incidental.
- **ARCHITECTURE §5 — an evicted layer directory is renamed out of the layer
  store before being deleted, and in-flight transactions pin their layers**
  (file updated). Deleting a directory in place lets a racing reader open it
  between the two file removals and see an index without its sidecar; the
  rename makes the whole directory disappear in one step, which is what §5's
  "old-or-gone, never torn" actually requires. Separately, a layer an open
  ingest has committed is referenced by no image record yet, so the refcount
  had to include open transactions or the evictor would delete it in exactly
  the window the ingest needs it.
- **The comparison LRU's single flight is hand-rolled, not
  `golang.org/x/sync/singleflight`.** It is ~40 lines shared with the LRU's own
  lock, it lets a waiter abandon its wait on request cancellation while the
  leader keeps working for the others, and it keeps `x/sync` an indirect
  dependency. The test hook that counts real assemblies
  (`server.WithAssemblyCounter`, `export_test.go`) is what makes
  "N concurrent identical requests do one unit of work" assertable as a fact
  about work done rather than a guess about timing.
- **`go-containerregistry` is now a direct non-test dependency.** Phase 004
  added it as a test-only checker; `internal/ingest` uses `pkg/v1/layout` to
  read the vendored fixture layouts, per ARCHITECTURE §2's rule that only
  `ingest` and `imgref` may import it. No gcr type escapes the package: the
  layout reader returns domain-typed records, and `v1.Image` appears only as an
  argument handed straight back into `Ingest`.
- **`internal/server/errors.go` split out of `server.go`** (phase file's
  requested layout). The §6.1 code table, envelope and `WriteError` moved
  verbatim; the new helpers (`badRequest`, `imageNotFound`, `writeStoreError`,
  `writeJSON`) are the only additions, and there is still exactly one error
  mechanism.
- **`cache_full` maps to HTTP 507** as §6.1's table says; the store's
  `ErrCacheFull` is what carries it up from `internal/cachestore` through
  `internal/ingest`. Phase 008's pull endpoints reuse the same error, unchanged.

### Backend review fixes (phases 002–005), 2026-08-30

Fixes for `REVIEW-backend-002-005.md`. Every wire change is **additive**: one new
optional field, no rename, removal or reinterpretation of an existing one.

- **ARCHITECTURE §6.5 — the depth=2 payload bound was self-contradictory** (file
  updated). The spec sanctioned `limit × (1 + limit)` rows per request and, three
  lines later, "the default page is ≤ ~70 KB". Both could not be true:
  `depth=2&limit=1000` embedded `min(limit, len(children))` grandchildren under
  each of 1000 rows — a million rows and a **311 MiB** body from one legal
  request, materialized whole by `json.Encoder` before a byte is written, against
  §4.6's 1.5 GiB RSS ceiling, concurrency-multiplied. The embedded half now has
  caps of its own, independent of `limit`: **≤ 50 children under any one row** and
  **≤ 2000 embedded rows across the response**, spent in row order. Measured on a
  1000×60 tree at `depth=2&limit=1000`: **22,014,264 → 1,088,784 bytes**
  (61,000 → 3,000 rows); the widest fixture directory at the same parameters is
  290,390 bytes. `childrenTruncated` already meant "page this directory yourself",
  and now also covers "the budget ran out and you got none", so nothing is lost —
  only a prefetch is shortened. `TestTreeDefaultPageIsBounded` measured only the
  *default* page, which is why the spec's own contradiction never failed a test;
  `TestTreeMaxLegalRequestIsBounded` measures the maximum legal one.
- **ARCHITECTURE §4.6 — "transient per layer" was not what the code did** (file
  updated). `Server.squash` loaded every layer index into a slice before calling
  `analyze.Squash`, so peak was Σ over layers rather than one layer's worth. New
  `analyze.Squasher` (`NewSquasher`/`Apply`/`Tree`) applies each index and drops
  it; `analyze.Squash([]LayerIndex)` stays, now expressed in terms of it, for its
  existing callers and tests. Measured at 50k entries/layer, live heap at the last
  layer: 5 layers **47 → 14 MiB**, 30 layers **343 → 14 MiB** — flat in the layer
  count, where one index is ~11 MiB. Both sides of a comparison put the review's
  512 MiB at roughly 28 MiB.
- **ARCHITECTURE §5's "a mutex per layer digest" did not exist** (file updated
  with what it now means). `Txn.PutLayer` was check-then-act: two transactions
  putting the same DiffID both missed, both reserved, both installed, and one map
  entry overwrote the other — so the cache was charged twice for one directory and
  refunded once on eviction. The drift is monotonic and ends in `507 cache_full`
  on an empty disk. The lock is now held across the *whole* of `PutLayer` (a lock
  released before the install leaves the same window), refcounted so the map holds
  only digests being written right now, with a redundant re-check under the store
  lock that releases the loser's reservation. Unreachable while fixtures ingest
  sequentially; normal once phase 008 pulls concurrently.
- **ARCHITECTURE §3.1 — the changeset digest sorts by `(Path, Kind)`** (file
  updated). It sorted by `Path` alone with an unstable sort, so a layer holding
  both an object and its whiteout marker at one path — the standard
  "delete x, then recreate x" representation — hashed differently depending on
  input order, contradicting its own doc comment. `indexState.finish` already
  sorted by `(Path, Kind)`, which masked it in production. The digest is now a
  property of the changeset, as claimed. No fixture digest moves: the indexer was
  already supplying entries in the new order.
- **Whiteout parsing rejects `.`/`..` and skips `.wh..wh.`** (ARCHITECTURE §4.1
  pseudocode updated). `dir/.wh..` trimmed to `"."`, which `path.Join` normalized
  away into a whiteout of the *parent directory* — one malformed member deleting a
  whole subtree of every lower layer. And aufs bookkeeping members (`.wh..wh.plnk`,
  `.wh..wh.orph`, `.wh..wh.aufs`) became phantom whiteouts of `wh.plnk` and
  friends, inflating `entryCount` and moving the changeset digest of a layer whose
  filesystem content is unchanged. Docker's `pkg/archive` skips that prefix
  outright; so do we, after the `.wh..wh..opq` check, which shares it.
- **ARCHITECTURE §3.2's "structurally impossible" is now structural** (file
  updated). `fieldsOfNode` zeroed `Size`/`ContentSHA` for directories and
  `fieldsOfEntry` did not, so the digest and the modification predicate could
  disagree about a directory. Both now call one `projectDir`. No digest moves in
  practice — the indexer never sets either field on a directory — but the
  guarantee is a function rather than two lists that happen to agree.
- **The dir↔file exception is documented rather than papered over**
  (ARCHITECTURE §6.5). A path that is a directory on one side and a file on the
  other is a leaf (§4.3, matching overlay semantics), so the vanished subtree's
  bytes are in neither side's `leftBytes`/`rightBytes` — and the root total
  therefore need not equal the image's total bytes. **Chosen: document, not
  count.** Folding the hidden bytes into `leftBytes` while excluding them from the
  change breakdowns would put bytes in a total that no reachable row accounts for
  and break §4.4's `Agg == Σ children.Agg`, which is exactly what lets a client
  reconcile a parent against the page it just expanded. The client can already
  detect the case from the wire — `status: "modified"`, `left.kind: "dir"`,
  `right.kind` something else — and label the row; `diff_test.go` pins the
  behaviour so it stays a decision.
- **WIRE (additive): `TreeSideMeta.implicit`** (ARCHITECTURE §3.2/§6.5 updated).
  A directory no layer header named reached the wire carrying the 0755 `ensureDirs`
  invents, indistinguishable from a real mode: an explicit `/d` (0700) against an
  implicit one rendered `unchanged` with `right.mode = 0755`, a value that exists
  nowhere but in our own bookkeeping. `implicit` is carried from `domain.Node`
  through `domain.SideMeta` onto `TreeSideMeta`, `omitempty` so absent means false
  and every existing row is byte-identical. It is deliberately not part of the
  tarsum-v1 field set and still never marks a row modified.
- **An evicted-layer 404 names the image it belongs to.** `handleDiffTree` passed
  `left.ID` to `writeStoreError` unconditionally, so a client whose *right* image
  was evicted was told to refetch the left one. `squash` now wraps its failure in
  an `imageError` carrying the record's id, and `writeStoreError` prefers it.
- **`Txn.Commit` validates "declared by this transaction", not `HasLayer`.**
  Presence is not the property that matters: only membership in `t.layers` holds a
  layer against the evictor, so an undeclared-but-present layer could be deleted
  between the check and the record rename — producing the record-without-its-layers
  state the startup sweep exists to clean up. Error message changed from
  "references uncommitted layer" to "which this ingest never put or used".
- **`Ingest` upgrades provenance instead of returning early** (new
  `cachestore.Store.UpgradeProvenance` + `cachestore.Provenance`). An image a
  registry pull fetched first and a fixture load found second stayed unpinned —
  exactly the image the LRU must not delete. Merge rules: **pinning is monotonic**
  (set, never cleared — it means "must survive eviction"), `source` records how the
  image most recently arrived, `refNames` union in order. A merge that changes
  nothing rewrites nothing, because the fixture load runs on every startup and must
  not churn ten record files per boot.
- **`Options.AllowedRegistries` is wired from `cmd/layerlens`** so
  `/api/v1/meta.allowedRegistries` is no longer permanently `[]` (ARCHITECTURE §6.6
  updated). The list is §7.1's allowlist, verbatim, as a package-level default in
  `main.go` — no new flag, no `--allowed-registries` surface to commit to. Phase 008
  owns the matching rule and the enforcement point; this only makes the field honest.
- **`comparisonCache.get` detaches the assembly context itself.** Its comment
  claimed a cancelling waiter could not affect the others, which was true only
  because `handleDiffTree` happened to pass `context.WithoutCancel`. A caller that
  honoured the parameter would let one disconnecting client cancel every waiter.
  `get` now applies `WithoutCancel` around `assemble`, and the handler passes its
  request context straight through — the code and the comment match, from either
  direction.
- **Query integers are parsed strictly.** `strconv.Atoi` accepts `+1`, `-0` and
  leading zeros, so `limit=+1` and `limit=1` were the same request under different
  spellings. `atoiStrict` accepts one spelling per value, applied to `depth`,
  `limit`, `leftLayers` and `rightLayers`.

### Phase 006 (frontend: app shell, selection view, layer comparison), 2026-08-30

- **CSP gains `style-src-attr 'unsafe-inline'`.** This is the option the phase
  001 note (and the comment in `internal/webui/webui.go`) already recorded as
  the right one; phase 006 is where it becomes necessary, a phase earlier than
  predicted. Two things need style *attributes*: Radix positions its portalled
  popovers and tooltips with inline styles, and the layer diagram places the
  could-be-shared pills, the selection rules and the relative-size bars from
  measured card geometry. What did **not** change: `style-src` stays `'self'`,
  so `style-src-elem` still inherits it and neither a `<style>` element nor a
  remote stylesheet can be injected; `script-src` stays `'self'`; and
  `img-src 'self' data:` denies the `url()` fetch that is the main thing a
  hostile style attribute could otherwise attempt. `internal/webui`'s CSP test
  now asserts the exact shape rather than a blanket "no unsafe-inline", and
  `bundle.test.ts` no longer asserts zero `[style]` attributes (it still
  asserts zero inline `<script>`/`<style>` elements).
- **The SVG overlay itself needs no style attributes.** Everything it draws is
  SVG presentation attributes (`d`, `viewBox`, `stroke-*` via CSS classes), so
  the diagram's geometry is CSP-clean; only the HTML elements layered over it
  (pills, rules, bars) use inline positioning.
- **Three ARIA radiogroups, not two.** DESIGN §7 asks for two radiogroups with
  the trunk cards belonging to both, acknowledging in the same sentence that
  this must be "one composite widget". A DOM node can only sit in one group, so
  the shipped structure is: a trunk group labelled "Shared comparison point
  (sets both images)", plus "Image A comparison point" and "Image B comparison
  point" for the branches; every trunk card carries `aria-describedby` pointing
  at "Shared layer — selecting it sets both comparison points". The keyboard
  map of §7 is implemented across all three as one roving-tabindex composite
  (↑/↓ within a column and across the trunk/branch boundary, ←/→ between the
  A and B columns, Home/End, Space/Enter to select). This is a truer rendering
  of the intent than nesting the same node in two groups would have been.
- **Docker and Registry tabs render disabled rather than being omitted.** The
  phase file said the tab bar should show only Analyzed ("no dead placeholder
  tabs"); the shipped bar shows all three, with the two phase-008 sources
  `disabled` and carrying a "soon" chip. Reason: DESIGN §4.3 requires the
  segmented control to be built so phase 008 adds panels "without layout
  shift", which only holds if the tabs are already at their final size — and a
  `disabled` control with a status chip is not a dead placeholder, it is an
  honest "not yet". They are unreachable by pointer and by keyboard.
- **Layer selection writes the URL with `replace`.** Adjusting a comparison
  point is a refinement of the same view, not a new destination; without
  `replace` the browser Back button would walk every card click before leaving
  the page.
- **`path` and `filter` round-trip through the codec untouched.** Nothing reads
  them until phase 007, but `urlstate.ts` parses, normalizes (`//app/` →
  `/app`) and re-serializes them, and `ComparePage` carries them through every
  selection change — so a link written today keeps working when the tree lands.
- **Instruction cleaning is mirrored client-side.** The server already sends a
  cleaned `instruction`, so `lib/instruction.ts` is a display fallback rather
  than a second source of truth; its Vitest table covers the same forms as
  `TestCleanInstruction` in `internal/analyze/history_test.go` so the two
  cannot drift silently.
- **New npm dependencies**, all previously named in this document except the
  last two: `wouter` 3.10.0 (ARCHITECTURE §8), `@radix-ui/react-popover`
  1.1.23 and `@radix-ui/react-tooltip` 1.2.16 (C2), plus
  `@testing-library/user-event` 14.6.1 and `@testing-library/jest-dom` 6.9.1 as
  dev-only test ergonomics. shadcn's CLI was not used: the two primitives we
  need are thin enough that vendoring them by hand (`components/ui/*.tsx`) is
  clearer than importing a generator's conventions for two files.
- **The filesystem-diff column is a designed skeleton, not a stub.** Its panel
  chrome is real — heading, breadcrumb root, legend, and a comparison line fed
  by the live selection — with DESIGN state #18's skeleton in the tree body.
  While the layer graph is still loading it shows *no* layer numbers at all
  rather than "A @ base", because naming a layer that has not been read yet
  would be a guess.


### Phase 007 (frontend: filesystem diff tree + golden-workflow e2e), 2026-08-30

- **The filter menu's five values all live in the URL; only two reach the API.**
  ARCHITECTURE §8.3 lists `filter` as a URL parameter and §6.5 defines the
  server's `all|changed`. DESIGN §5.3's menu has five entries. Shipped:
  `TreeFilter` is `all | changed | added | removed | modified`, and
  `serverFilter()` maps the last three onto `changed` — so all four non-`all`
  filters share one query key and one set of cached pages, and the whole menu
  is still shareable by link. The refinements are applied client-side to rows
  the server already returned, which is exactly as far as they can go without
  the search endpoint §6 does not define (below).
- **Disclosure resets on a pair change, not on a selection change.**
  ARCHITECTURE §8.3 said "reset when pair/selection changes". Shipped: only the
  pair resets it. A layer selection changes what the rows *say*, not which
  paths exist, and collapsing the user's expansion on every nudge of a
  comparison point defeats state #24's whole point — the dim-and-keep
  treatment is only visible if there is something left to dim. §8.3 updated.
- **Playwright lives under `web/`, not at the repository root.** The phase file
  put `playwright.config.ts` and `e2e/` at the root; the repo has exactly one
  npm project, so `@playwright/test` is unresolvable from there. Shipped:
  `web/playwright.config.ts` + `web/e2e/`, with `webServer.cwd = ".."` so the
  binary and `fixtures/` still resolve from the root. `mise run e2e` depends on
  `build` and runs `playwright test` in `web/`. Adding a second `package.json`
  purely to host a config would have been the worse trade.
- **`depth=2` is a first-page-only prefetch, and it seeds sibling query keys.**
  §8.4 step 4 asks for the root to be prefetched at depth 2. Shipped: *every*
  directory's first page is requested at depth 2 and subsequent (cursor) pages
  at depth 1 — paging a 2 500-child directory must not re-pay for grandchildren
  — and each returned row whose children are complete is written into that
  child's own `['tree', …]` key, so the first expand is a render rather than a
  round trip (asserted in `e2e/golden.spec.ts`). Two rules make that safe:
  a row with `childrenTruncated` is **never** seeded (a prefix under
  `staleTime: Infinity` would strand the directory with no cursor to continue
  from), and nothing is seeded while `isPlaceholderData` is true.
- **The placeholder-seeding bug this found.** `placeholderData: keepPreviousData`
  means `query.data` can be the *previous* key's answer while the new one
  loads. That is right for rendering and wrong for anything that writes to the
  cache: the first implementation filed the `filter=changed` children under the
  `filter=all` key, where `staleTime: Infinity` kept them forever, so switching
  the filter left every expanded directory showing the old filter's rows. Any
  future cache write from a query with `keepPreviousData` needs the same guard.
- **First paint opens the single-child spine.** Neither document specifies an
  initial expansion; the approved prototype hardcodes `/app` open. Shipped: on
  the first loaded page of a comparison, the tree walks open any chain of
  directories that has exactly one child worth opening, stopping at the first
  branch and at depth 3. Under the default "changed only" filter that is
  reliably the one directory the diff hangs off. It runs once per
  (pair, selection, root, filter) and never re-opens what a user collapsed.
- **The name filter searches the query cache, not just the mounted tree.**
  The phase file's risk note stands — DESIGN §5.3/§10.5 want server-assisted
  search and ARCHITECTURE §6 defines no endpoint for it, so this remains a
  **client-side substring filter over fetched rows** and a real search endpoint
  is still an open API addition. What shipped goes one useful step further than
  "loaded rows": it reads `queryClient.getQueryData` for any path, so the
  depth=2 prefetch lets a search for `util` find `/app/src/util.js` while
  `src/` is still collapsed, and auto-expands the ancestor chain to show it.
  The empty state says plainly that the filter searches what has been fetched.
- **"Show N more…" and the watermark both ship.** DESIGN §5.3 wants a
  button-styled trailing row; ARCHITECTURE §8.4 wants a virtualizer watermark
  that calls `fetchNextPage`. Shipped: both. The trailing row is a real button
  that reads `Show N more…` at rest and `Loading more…` while a page is in
  flight, and the watermark starts the next page ~12 rows before the row
  enters the viewport. In practice the button is transient during smooth
  scrolling and becomes the manual control whenever the watermark has not
  fired — which is also why it is the surface state #27's `Retry` replaces.
- **A failed *page* never blanks a loaded directory.** The whole-panel error
  state only applies when the directory has no rows at all; a cursor page that
  fails puts an inline error row with `Retry` where the "more" row was, and the
  rows already loaded stay. Asserted in `e2e/pagination.spec.ts`.
- **State #25 is a claim about the comparison, so it is only made at `/`.**
  An all-unchanged *nested* directory renders state #26 ("no changes in this
  directory"), not "the filesystems are identical at these layers" — the latter
  would be false wherever the user had drilled to a quiet corner of a
  comparison that does differ.
- **#26's hidden count is fetched, not guessed.** The number of unchanged
  entries is not derivable from a `changed` response, so the empty state mounts
  one `filter=all` query for the same path — which also warms the exact cache
  entry its "Show all entries" button switches to. When rows *are* showing, the
  panel's footer note carries no number at all rather than a fabricated one.
- **"showing N of M entries" counts the current directory.** DESIGN §5.3's
  example ("214 of 48,112") implies a whole-tree total that a windowed API
  cannot produce. Shipped: N is the loaded, post-filter direct children of the
  current root and M is the server's `totalRows` for it — which is the number
  that actually answers "is this directory fully loaded?" while paging.
- **`aria-setsize` comes from the server, and is dropped when it cannot.** Rows
  carry the parent's post-filter `childCount`, not the number of rows paged in,
  so a screen reader in a 2 500-child directory hears "3 of 2,500". Under a
  client-side refinement or a name filter that number is no longer the honest
  set size, so the attribute falls back to headless-tree's own count of what is
  visible.
- **Two layout bugs the tree exposed, both pre-existing.** `#root` had no
  height, so the `h-full` chain from `<html>` stopped at the mount point and
  every "fill the viewport, scroll inside" panel silently became "grow with the
  content, scroll the page"; and the compare grid had no explicit row track, so
  the row grew to the tallest column. Either alone makes the virtualizer
  measure a viewport the size of the whole list — it rendered 61 rows where it
  should render ~29. Fixed with `#root { height: 100% }` and
  `grid-rows-[minmax(0,1fr)]`.
- **jsdom needs two shims for the virtualizer, and they are deliberately
  narrow.** TanStack Virtual reads `offsetWidth`/`offsetHeight`, which jsdom
  reports as 0, and needs a `ResizeObserver`, which jsdom lacks — so without
  help the tree renders zero rows and every assertion about it passes
  vacuously. `vitest.setup.ts` supplies a no-op observer and a viewport-sized
  box **for the tree's scroll container only**; everything else keeps jsdom's
  zeros. Real geometry (column alignment, wrapping, virtualization bounds) is
  asserted in `web/e2e/columns.spec.ts` against Chromium, which is the only
  place those questions have an answer.
- **`renderApp` gained a `gcTime` knob.** The suite default of 0 keeps tests
  isolated, but it also collects the depth=2 seed before anything observes it —
  so the one test that is *about* the prefetch asks for a real `gcTime`.
- **New npm dependencies:** `@headless-tree/core` + `@headless-tree/react`
  1.7.0 and `@tanstack/react-virtual` 3.14.10 (both named in §C3), plus
  `@playwright/test` 1.62.1 (§D). `eslint` reports one standing warning,
  `react-hooks/incompatible-library`, on `useVirtualizer`: it is a note that the
  React Compiler declines to memoize that component, and the compiler is not
  part of this build.
- **`components/ui/popover.tsx` gained optional controlled props.** The
  breadcrumb overflow menu has to close on its own selection, which an
  uncontrolled Radix popover cannot do. `open`/`onOpenChange` are optional, so
  the existing uncontrolled call sites are untouched.


### Phase 008 (remote sources: registry pulls, Docker ingest, SSRF), 2026-08-30

- **The plaintext refusal lives in the dialer, not only in `CheckRedirect`.**
  §7.2 put the no-downgrade rule on `http.Client.CheckRedirect`. That hook
  never runs on the path that matters: go-containerregistry builds its own
  `http.Client` (`remote/fetcher.go`) and only takes our *transport*. Shipped:
  `http.Transport.DialContext` — the hook net/http uses for `http://` — always
  returns `ErrPlaintextRefused`, and only `DialTLSContext` can connect. A
  downgrade is then unreachable regardless of whose client follows the
  redirect. `safehttp.Client()` still carries the 10-hop cap and the scheme
  check for requests layerlens issues itself. §7.2 updated.
- **`Proxy` is nil and stays nil.** `http.ProxyFromEnvironment` would route
  every request through an address the screen never sees, which on most hosts
  is a loopback one — the exact hole this package exists to close.
- **All vetted addresses are tried, not just `ips[0]`.** §7.2's pseudocode
  dials the first. Every candidate passed the identical screen (a single
  non-public answer already failed the whole host), so trying the second on a
  dead first is the same policy with better behaviour. §7.2 updated.
- **The screen covers more than the §7.2 list.** Beyond loopback/private/
  link-local/multicast/unspecified/ULA/IPv4-mapped it also refuses CGNAT
  (100.64/10), 0.0.0.0/8, the TEST-NETs, 198.18/15, 240/4, Teredo, and — the
  one that actually matters — NAT64 (`64:ff9b::/96`) and 6to4 (`2002::/16`),
  whose embedded IPv4 is unwrapped and re-screened. `64:ff9b::a9fe:a9fe` is an
  ordinary-looking IPv6 address that a NAT64 gateway delivers to
  169.254.169.254.
- **Size caps are inverted: everything is capped except a blob fetch.** §7.2
  asks for 8 MiB caps on "manifests, indexes and configs". A config is fetched
  as a blob, and after the CDN redirect its URL is not even on the registry's
  host, so no path rule can tell it from a layer. Shipped: the transport caps
  every response whose path is not a blob fetch (manifests, indexes, tokens,
  the `/v2/` ping) plus any blob whose digest the caller has declared small —
  `Transport.ExpectSmallBlob(digest)`, which the registry source holds around
  the config read. Belt and braces, the manifest's *declared* `config.size` is
  refused above 8 MiB before the config is fetched at all, and the layer count
  is capped at 512 before any blob is opened. Residual, documented: if a
  registry's CDN dropped the digest from the redirect URL, that image's config
  would be uncapped rather than the pull breaking — failing open on
  availability, closed on the declared-size check.
- **`safehttp.Options` carries two test-only seams: `PermitLoopback` and
  `RootCAs`.** A hardened dialer is untestable without one of them, and the
  alternative (an `httptest` server the tests reach by disabling verification)
  would have been worse. `PermitLoopback` relaxes the address screen and the
  port rule for loopback only; `RootCAs` narrows trust rather than widening it,
  and `InsecureSkipVerify` appears nowhere in the tree. `cmd/layerlens` sets
  neither, and a test asserts the default refuses a loopback server.
- **The docker save stream is parsed in one pass with no spool file.** The
  phase file said "streamed to a staging spool and parsed with gcr
  layout/tarball readers". A spool is a second copy of a 25 GiB image *and* is
  charged against `--cache-max-bytes` (§5), which would make the daemon path
  fail on exactly the images it exists for. It also buys nothing: an Engine 29
  save writes `index.json` and `manifest.json` **after** the blobs (verified
  against the real daemon), so no reader gets to look ahead. Shipped: members
  small enough to be metadata (≤ 8 MiB) are buffered; anything larger is
  indexed as it streams, with its DiffID *computed* rather than declared and
  its compression sniffed from the magic bytes; at EOF the config's
  `rootfs.diff_ids` reconcile the pass, indexing from the buffers any layer
  small enough to have been buffered. Memory stays bounded (≤ 64 MiB of
  metadata plus one layer's index) and no blob is ever written to disk.
  Consequence: a big member that is not a layer is a logged warning and a
  discarded index, not a failed ingest.
- **Cheap draining of known layers applies only when the metadata precedes the
  blobs.** DECISIONS A2 asks for known layers to be drained rather than
  re-hashed. In a save stream a blob cannot be identified before it is read
  unless the manifest *and* config have already been seen, which the real
  Engine ordering makes uncommon. Shipped: the parser re-resolves its target
  after every buffered member and drains a layer it can already identify
  (tested both ways) — and, far more importantly, the manager prefers the
  registry path outright whenever `docker inspect` reports a `RepoDigests`
  entry on an allowlisted registry, where skipping is free and exact.
- **No per-layer checkmark list — DESIGN §4.4 asks for one the API cannot
  feed.** `PullStatus` (§6.3) carries counts plus the *current* layer, not an
  array of layers, and widening it would mean streaming a 512-entry array on
  every 800 ms poll to render a list nobody reads during a pull. Shipped: the
  current layer with its own byte counts, `n of m layers`, and the
  already-analyzed count — which is the part of that list that carries
  information. DESIGN §4.4's "per-layer checkmark list (collapsed behind
  details beyond 10 layers)" is deliberately not implemented.
- **No server-side progress throttle.** The phase file suggested throttling
  status updates to ~100 ms. Shipped: byte progress is an atomic counter that
  `analyze.IndexLayer` increments and the poller reads, and per-layer
  transitions take a mutex once per layer. The hot path never takes a lock, so
  there is nothing for a throttle to protect, and a throttle would only make
  the reported number stale.
- **`--docker-host off` disables the daemon source.** §1.3 had no opt-out, so
  on any host with a socket the Docker tab was whatever that machine happened
  to hold — which makes the Playwright suite non-deterministic on a developer
  laptop and gives an operator no way to run a pull-only server. §1.3 updated.
- **`domain.DockerListing` reshaped to §6.2.** The phase-005 placeholder had
  `{id, refNames, size}`; the contract is `{reference, dockerId, sizeBytes,
  alreadyAnalyzed, analyzedId}` plus `platform` (DESIGN §4.3 asks the row to
  show it) and `reason`. §6.2 updated with `platform`.
- **"Already analyzed" is matched by two keys, not one.** The daemon's image
  id is normally the config digest, which is layerlens' own id — but under the
  containerd image store a multi-platform image is identified by its *index*
  digest, which matches nothing in the cache (observed against the real Engine
  29 daemon in this sandbox: `alpine:3.20` lists as `sha256:d9e853e8…` while
  its analyzed id is `sha256:bf8527eb…`). The listing therefore also matches on
  the display reference, which every ingest records.
- **An already-cached image still reports a finished pull.** `Ingest`'s
  already-present branch used to report no progress at all, leaving the UI with
  a card stuck at zero. It now announces the layer count and marks every layer
  skipped, which is the honest reading of what happened.
- **`Ingester.Start` returns `StartResult{ID, Created}`.** §6.3 distinguishes
  202 (started) from 200 (already in flight, or already cached), which a bare
  `PullID` cannot express. Untagged daemon images are omitted from the listing:
  a row whose reference cannot be submitted back is a dead row.
- **Two pull failure codes beyond the §6.1 table.** `PullStatus.error` is not
  an HTTP envelope, and DESIGN states #11 and #12 need to be distinguishable:
  `pull_rate_limited` and the catch-all `pull_failed`, alongside the table's
  `pull_upstream_denied`, `cache_full` and `docker_unavailable`. The catch-all
  message is deliberately generic — an upstream's error text is logged, never
  rendered.
- **The allowlist verdict precedes the scheme check in `imgref.Parse`.** So
  `localhost/x` and `127.0.0.1/x` are reported as "not on the allowlist", which
  is what a user needs to hear, rather than as an http-scheme technicality. An
  explicit port is still `invalid_reference` (a port is a different service
  from the one the operator vetted, not a different registry). Registry hosts
  are lowercased and stripped of a trailing root dot before any of it, so
  `GHCR.IO` and `ghcr.io.` cannot be a second spelling.
- **`/api/v1/meta.allowedRegistries` now comes from `imgref.DefaultPatterns`,
  not from a copy in `main.go`.** The phase-005 note above put the list in the
  command as a placeholder. Now that the matching rule exists, the displayed
  list and the enforced list are the same variable and cannot drift.
- **The accept/reject table is one JSON file, `testdata/refs.json`, read by
  both implementations.** `internal/imgref/imgref_test.go` and the SPA's
  `refcheck` mirror are driven from it, so the inline verdict the user sees
  while typing cannot drift from the verdict the server will give. The client
  adds one verdict the server has no use for — `empty`, for an untouched field,
  which must not render as a red error.
- **A Docker reference is validated on the raw string, not only by parsing.**
  A local reference is not allowlisted — the daemon is local trust — but the
  Engine client builds its URL by concatenating the reference into
  `/images/<ref>/json`, and go-containerregistry's grammar accepts `.` and `..`
  as path segments (`./x` even parses as a *registry* called "."). Nothing
  useful is reachable that way — the path always ends in a literal segment and
  carries no query parameters — but a reference with a traversal segment is not
  one anyone means to type, so it is refused, and the save stream is opened
  against the id `docker inspect` returned rather than against the user's
  string. Found by the security review pass over this phase.
- **Pull cards live above the tab strip, not inside a slot card.** DESIGN §4.4
  puts a compact progress ring on the slot card and mirrors the pull into the
  source panel. Shipped: an image joins a slot only once it is analyzed and
  selectable, so a pull has no slot to ring; the cards sit above the tab strip
  instead, which satisfies the same requirement — "the user may switch tabs
  while a pull runs and never lose it from view" — structurally rather than by
  remembering state. A Docker row's `Analyze` button starts the same pull
  rather than DESIGN §4.3's "selecting it kicks off analysis at Compare time":
  work that starts when you press a button labelled for it beats work that
  starts two clicks later somewhere else.
- **The Docker list carries a footnote when a row's platform is not
  linux/amd64.** The daemon reports the variant it would *run* — arm64 on an
  Apple-silicon or arm64 host — while layerlens always analyzes the amd64
  variant, which the same image usually also holds. Without the note a row
  reading `linux/arm64` that analyzes successfully looks like a bug.
- **`friendlyRegistryNames` keeps DESIGN §9 #8's exact sentence** ("Allowed:
  Docker Hub, GHCR, GCR, ECR, ACR"), which folds `*.pkg.dev` under "GCR". The
  full pattern list is also printed verbatim under the input, so an Artifact
  Registry user sees `*.pkg.dev` there and gets a `✓ allowed` verdict on the
  reference itself.

### Phase 009 (deployment & release hardening), 2026-08-30

- **ARCHITECTURE §9.5 item 14 was written against the wrong unit of measure.**
  It said: start with `--cache-max-bytes=1000000` and "ingest a fixture larger
  than that". The cache stores layer *indexes*, never blobs (§5), so the cap
  bounds index bytes: with a 1,000,000-byte cap the UAT actually ingested a
  57 MB image successfully (its index is a few hundred KB). Executing the
  item's *intent* needs a cap just above what the pinned fixtures occupy
  (~195 KB). **§9.5 item 14 updated** to say so, with the unit-of-measure
  caveat spelled out; the refusal path itself behaves exactly as RESEARCH Q7
  requires — verified: cap 250000, fixtures at 195256, ingest of
  `node:22-alpine` refused with `cache_full` ("This image does not fit in the
  server's cache budget.") and all ten pinned fixtures untouched. The same
  caveat is now in the README's known limitations, because sizing the cap
  against image sizes would over-provision it by three orders of magnitude.

- **`--docker-host off` was indistinguishable from "no socket found".** Both
  left `cfg.dockerHost` empty, so the daemon tab told the user "No Docker
  socket found at /var/run/docker.sock" even when the operator had explicitly
  turned the source off. This mattered because the systemd unit this phase
  ships sets `off` by **default** (the socket needs a root-equivalent group),
  so every deployment would have shown the wrong explanation. Fixed:
  `ingest.DockerOptions.Disabled` carries the operator's decision through to
  `unavailableReason`, which now answers "The Docker daemon source is turned
  off on this server (--docker-host off)." **ARCHITECTURE §1.3 updated** to
  record that the two states are distinct. Covered by
  `TestDockerListingWhenExplicitlyDisabled`.

- **The unit parameterizes its flags through `Environment=` +
  `EnvironmentFile=-/etc/layerlens/layerlens.env`** rather than hardcoding them
  in `ExecStart` as §1.3 sketched. Reason: the deploy overwrites the unit file
  every time, so an operator who tuned `--cache-max-bytes` on the box would
  lose it on the next deploy. §1.3 updated.

- **Fixture swap is not atomic, and cannot be.** The binary swap is a single
  `rename(2)` into the same filesystem (staging path inside the install
  directory) and is genuinely atomic; a directory has no such primitive, so
  `fixtures/` is swapped in two steps keeping one previous copy. Harmless: the
  server reads fixtures once at startup, and the restart follows the swap.

- **Not fixed, reported instead (out of phase-009 scope):** layer indexes
  committed by a pull that never produces an image record — one refused with
  `cache_full`, or cancelled and never retried — are counted against the cache
  cap and are never reclaimed. Eviction only drops layers when an *image* that
  references them is evicted (`lru.go` `layerReferencedLocked`), and the
  startup sweep deliberately keeps complete-but-unreferenced layer directories
  because that is what makes a retry cheap. Observed during UAT: a refused pull
  left ~7.8 KB behind. Bounded in practice (kilobytes per abandoned pull against
  a 50 GiB default) but unbounded in principle; the fix is an orphan-layer
  reclamation pass in `cachestore`, which is phase 005 surface.

### Security review fixes (phase 008 adversarial review), 2026-08-30

Applied from `REVIEW-phase-008-security.md`. The review's verdict — "safe on a
trusted network as specified; not safe if exposed publicly, and the reason is
availability, not SSRF" — is unchanged in kind: none of these are exploits of
the trust boundary, they are missing bounds on work. Every finding marked
`[repro]` was re-reproduced before and after the fix.

- **H1 — entry count per layer is now capped** (ARCHITECTURE §4.6, §7.2
  updated). `analyze.IndexLayer` grew `entries` without limit. `MaxLayers`,
  manifest size and config size were capped; entry *count*, the one input that
  is O(retained memory), was not. Reproduced at **14.8 MB of gzip → 1,905 MB of
  heap, 129:1** (the review measured 7.0 MB → 494 MB, 74:1 with a different
  member shape). New `analyze.DefaultMaxEntries` = 2,000,000, injectable via
  `LayerSource.MaxEntries` / `ingest.Options.MaxLayerEntries` /
  `--max-layer-entries`, refusing with `analyze.ErrTooManyEntries`. After: the
  same blob is refused with heap flat at the cap. The cap counts **distinct
  paths** (last-in-tar-wins costs one) and charges whiteout/opaque markers too,
  and it fires on the header rather than after draining the member's body.
- **H1 wire surface — new pull failure code `pull_too_large`.** `ErrTooManyEntries`
  and the pre-existing `ErrTooManyLayers` both classify to it. `ErrTooManyLayers`
  previously reported `pull_failed`; the message was already specific, only the
  code changes, and unknown codes already fall back to a generic heading in the
  SPA. Added to ARCHITECTURE §6.1 and to `PullProgress.tsx`'s heading map.
- **H2 — admission control on pulls** (ARCHITECTURE §6.3, §1.3 updated). Every
  POST bought a goroutine and a real outbound registry session with nothing in
  between. Reproduced: **400 concurrent submits → 400 accepted, 400 goroutines,
  399 outbound sessions, pull table of 400** despite `maxRetainedPulls = 64`
  (`evictLocked` only drops *terminal* pulls, so the cap could never bite while
  everything was live). After, with the default limit of 4: **4 accepted,
  396 refused with 429 `too_many_pulls`, 4 outbound sessions, table of 4.**
  The duplicate check and the slot acquisition now happen under one hold of the
  manager mutex, which is what makes idempotent resubmission free *and* stops
  two identical concurrent submits from both being admitted — the old
  check-then-launch had that race too.
- **M1 — `--cache-max-bytes` now bounds peak disk, not just steady state**
  (ARCHITECTURE §5 updated). The staged index was written in full and only then
  measured. Reproduced at a 1 MiB cap: **20,173,648 bytes on disk (19.2x)**
  before `ErrCacheFull` (the review measured 12,104,078, 11.5x, on a smaller
  index). The staged write is now charged against `cap − pinned − txn.ownBytes`
  as it streams, with the limiter *under* the buffered writer so the overshoot
  is at most one flush. After: **1,046,367 bytes (1.00x)**. `reserve` still runs
  afterwards and still produces the error message, so the wording a user sees
  does not depend on which of the two checks fired first.
- **M2 — a throughput floor, deliberately not a total deadline** (ARCHITECTURE
  §7.2). `safehttp` bounded only time-to-first-byte and the manager ran on
  `context.Background()`. Every response body is now wrapped in a watchdog
  requiring `MinThroughput` bytes/s over a `StallWindow` (4 KiB/s over 30 s by
  default), which aborts the request context — the only mechanism net/http
  offers for unblocking a `Read` already parked in the kernel. A floor was
  chosen over a deadline because it cannot misjudge a slow-but-progressing
  25 GiB pull, which is the case any deadline generous enough for that image is
  also generous enough to hide inside. `--pull-timeout` (6h, `0` disables) is
  the backstop for what the floor cannot see, chiefly the docker-save stream;
  6h carries 25 GiB at ~1.2 MB/s sustained. A pull that hits it reports
  `error`, not `cancelled`: nobody asked for it.
- **L1 — `.`/`..`/empty path segments refused on the registry path**
  (ARCHITECTURE §7.1). The rule already existed ten lines away for the Docker
  path; it is now one exported helper (`imgref.ValidatePathSegments`) used by
  both, and mirrored in the SPA's `refcheck.ts` so the shared
  `testdata/refs.json` vectors stay honest.
- **L2 — trailing dots normalized exactly once** (ARCHITECTURE §7.1). `Parse`
  trimmed one and `Allows` trimmed a second, so `ghcr.io../o/i` was accepted and
  carried as the registry `ghcr.io.`. Normalization is now one function that
  trims one dot and then requires every label to be non-empty. The SPA mirror
  had the same shape and now agrees (it previously answered `not-allowed` where
  the server now answers `invalid`).
- **Informational, all four taken.** `--docker-host` must name a unix socket
  (path or `unix://`) unless `--docker-allow-tcp` is passed, including when the
  endpoint comes from `DOCKER_HOST` — the daemon path is the one egress the
  dial-time screen never sees. `5f00::/16` added to `reservedV6`, which its own
  comment already named. `MaxResponseHeaderBytes = 64 KiB` on the transport (the
  stdlib default is 10 MiB per response). Pull ids are now `"p" + 16
  crypto/rand bytes in hex` rather than `p<unixnano>-<seq>`: any client can
  still cancel any pull, which is acceptable on a trusted network and is what
  `GET /api/v1/pulls` already implies, but it need not also be guessable — and
  §7.3 had already specified random hex.
- **Not done: an auth gate.** The review names it as required *if this were ever
  exposed publicly*. It is out of scope per RESEARCH Q3 (private/trusted
  deployment) and is a product decision, not a bug fix; the four availability
  bounds above are the part that was in scope.


## Risks

1. **25 GiB images.** Never hold a layer in memory and never keep extracted filesystems.
   One streaming pass per layer (registry: `Layer.Uncompressed()`; daemon: `ImageSave`
   stream) producing a **per-layer metadata index keyed by DiffID** (entries: path, type,
   mode, uid/gid, size, link target, content-sha256, whiteout flags) stored as
   zstd-compressed JSONL (`github.com/klauspost/compress/zstd` — already a gcr dep).
   Sizing: ~150–200 B/entry raw → a 500k-file image ≈ 75–100 MB raw, **~15–30 MB
   compressed**; entirely tractable. Cumulative trees are merged in memory on demand from
   the per-layer indexes (sorted-list merge; ~100–200 MB transient for a 500k-file pair)
   with a small LRU of assembled pairs. Risks that remain: index build time for 25 GiB
   (network + sha256 throughput, minutes — needs progress UI + resumable per-layer
   checkpoints) and disk-full handling in the cache dir.
2. **SSRF.** Parse with `name.ParseReference` and allowlist on
   `ref.Context().RegistryStr()` exactly: `index.docker.io`, `ghcr.io`, `gcr.io`,
   `public.ecr.aws`, `*.dkr.ecr.*.amazonaws.com`, `*.azurecr.io` (no user-supplied ports,
   no HTTP/`--insecure`). Blob GETs legitimately redirect to CDNs (S3/GCS/Azure), so we
   cannot block redirects; instead inject a custom transport (`remote.WithTransport`)
   whose `DialContext` resolves and **rejects private/loopback/link-local/multicast IPs**
   (`ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || …`) on every
   connection including redirect hops, and cap redirect count. Also cap manifest/config
   sizes read into memory.
3. **arm64 dev vs amd64 deploy.** Pure-Go stack ⇒ `CGO_ENABLED=0 GOOS=linux GOARCH=amd64
   go build` from the arm64 sandbox or the Mac just works (no cgo anywhere in the chosen
   deps; avoid sqlite/cgo — another reason for the flat-file index). The analyzer always
   selects `linux/amd64` image content regardless of host arch — content parsing is
   arch-agnostic, so dev-on-arm64 is fine; only e2e docker-pull smoke tests must not
   assume host arch = image arch.
4. **BuildKit attestation manifests** (`unknown/unknown` platforms) in indexes — handled
   by `remote.WithPlatform`, but any custom index iteration (e.g. listing tags/platforms)
   must skip them or phantom "images" appear.
5. **History↔layer misalignment** on squashed/hand-built images — fall back gracefully
   (A5), never crash or mislabel.
6. **Docker Hub rate limits** for anonymous pulls (100 pulls/6h/IP as of last check) —
   cache aggressively by digest; the exe.dev deployment shares one IP.
7. **Could-be-shared cost**: per-file SHA-256 during indexing is CPU-bound (~1–2 GB/s/core);
   for 25 GiB that's minutes — acceptable one-time cost, but do it in the same pass as
   indexing, never a second pass.
8. **TS 7 / ecosystem drift**: pinning TS 5.9.3 is safe today; the pin is one line in
   package.json to revisit.

## Open questions for the user

> **ALL RESOLVED 2026-08-29.** Every question below has been answered by the user; the
> binding answers live in `.planning/RESEARCH.md` (Q1–Q8). Summary: anonymous-only
> registry auth; vendored deterministic OCI fixtures for the demo images; private/trusted
> deployment (no auth layer needed); configurable cache cap with LRU eviction; changeset
> digest = content + mode, excluding mtime and uid/gid. The questions are kept below for
> the record — read `RESEARCH.md` for what was decided.


1. **Private-registry credentials — in scope?** Anonymous public pulls work for Docker
   Hub, GHCR, GCR, ECR-Public, and anonymous-enabled ACR. Private ECR (no anonymous mode
   exists) and typical private ACR/GCR need credentials. Ship anonymous-only for MVP, or
   should the server read `~/.docker/config.json` (+ optional cloud keychains) on the
   deploy host? Affects auth plumbing, the SSRF story, and whether secrets live on exe.dev.
2. **How are the demo `example:v1`/`example:v2` images produced on the deploy host?**
   Options: (a) pre-generate deterministic OCI layouts with our fixture tool and ship
   them with the deploy (no Docker needed on exe.dev — my default); (b) require Docker +
   `docker build` of the sample Dockerfile at first boot (real `node:24` base ⇒ ~1 GB+
   download, non-deterministic). Does the exe.dev VM have Docker at all, and do you want
   the demo trunk to be a *real* node base image or a small synthetic one?
3. **Is the exe.dev deployment publicly reachable?** If yes, an unauthenticated UI that
   pulls & stores up to 25 GiB per request is a DoS/abuse vector — do we need an auth
   gate (basic auth / tailscale-style network restriction) and per-request size quotas,
   or is it firewalled to you only? Materially changes server hardening scope.
4. **Cache retention on the server**: metadata indexes are small, but how many analyzed
   images should we retain, and is there a disk budget on the exe.dev VM (drives eviction
   policy and whether "up to 25 GiB" source images may be re-fetched after eviction)?
5. **"Could-be-shared" strictness**: should ownership/permissions (uid/gid/mode) count as
   differences, or is equal content+paths enough for the dotted line? (Docker's own cache
   would treat them as different layers; content-only is more forgiving and arguably more
   useful for the ".dockerignore mistake" demo.) Default: include mode/uid/gid, exclude
   timestamps — confirm.

### Clean tasks, and a truncation bug they surfaced (2026-08-30)

`mise run clean` removes gitignored build output, `web/node_modules`, and local
runtime state, returning the tree to fresh-checkout equivalence; `clean-deep`
additionally wipes the shared Go build/test/module caches. They are separate
tasks because those caches live outside the repo and are shared with every other
Go project on the machine, so a `clean` that swept them would be surprising and
expensive. `clean` finishes by listing anything still ignored that it did not
delete, so the hand-maintained path list cannot drift out of step with
`.gitignore` unnoticed.

Two things worth recording:

1. **mise appends task arguments to the script's last line** rather than passing
   them as `$1`. An initial `clean --deep` implementation parsed `"${1:-}"` and
   silently did nothing — `mise run clean --bogus` appended `--bogus` to a
   trailing `echo` and exited 0. Hence two tasks rather than one flag. Any
   future task that needs arguments must use mise's `usage`/`{{arg()}}` support,
   not positional parameters.

2. **The cold rebuild after `clean-deep` exposed a real bug in the stall
   guard.** `TestStallDetectorRefusesATricklingBody` failed under the load of a
   from-scratch build with "An error is expected but got nil". The cause was not
   flakiness: `stallGuard.Read` excluded `io.EOF` from the tripped check, but
   cancelling the request makes the upstream hang up, and a tidy hang-up is
   delivered to this side as a clean EOF as readily as an error — which one wins
   is a race decided by machine load. On the EOF side the caller received a
   **silently truncated body reported as complete**. For a layer blob the DiffID
   check would eventually catch it; a manifest or config would simply be short.
   Once tripped, the transfer was killed deliberately and has no legitimate end,
   so every terminal outcome is now `ErrStalled`.

   The regression test for this is in-package (`stallguard_internal_test.go`) and
   sets `tripped` directly. Two successive attempts to test it over a socket
   both passed against the buggy code — one because `Content-Length` made the
   client report `ErrUnexpectedEOF`, which the old condition already caught, and
   one because request-context cancellation usually surfaces as an error rather
   than the EOF the bug needed. Only the EOF half of the race is interesting, so
   it is asserted at the guard where it is deterministic, and both tests were
   confirmed to fail against the old condition before being kept.

### macOS portability fixes (2026-08-30)

Three failures reported from a real `mise run clean` + rebuild on macOS that a
Linux run cannot produce. All were genuine; none were flakes. The lesson is that
"green" here had only ever meant "green on linux/arm64", while `PROJECT.md` puts
the e2e target on a Mac.

1. **`TestParseFlagsAutodetectsLocalSocket` — `bind: invalid argument`.** A unix
   socket address lives in `sockaddr_un.sun_path`, which is **104 bytes on macOS**
   against 108 on Linux, and macOS hands out per-test temp directories like
   `/var/folders/f7/w21qlq914rq45z5lxcfq7gkw0000gn/T/TestName1234567890/001` that
   overrun it unaided. `t.TempDir()` is still preferred where it fits; a
   `shortSocketDir` helper falls back to a short base only when it would not.
   Reproduced on Linux by running the old test under a 209-character `TMPDIR`,
   which yields the identical error, and confirmed fixed the same way.

2. **`deploy.sh` — `EXTRA_OPTS[@]: unbound variable`, failing four deploy tests.**
   Under `set -u`, **bash 3.2 treats an empty array's `[@]` expansion as
   unbound** (relaxed in 4.4), and macOS still ships bash 3.2 as `/bin/bash`.
   `LAYERLENS_DEPLOY_SSH_OPTS` is empty in the common case, so every deploy on a
   Mac aborted before printing a plan. Fixed by *not creating the empty
   expansion at all* — the options array is appended to only when the variable is
   non-empty — rather than by the `${ARR[@]+"${ARR[@]}"}` guard. Deliberate: bash
   5.3 does not reproduce the failure and `BASH_COMPAT=3.2` does not restore it,
   so a workaround whose correctness depends on a version difference could not be
   verified here, while the restructured form behaves identically on every bash
   from 3.1 up.

3. **Duplicate React keys `spine-a-M0 0 V0`.** `EdgeOverlay` keyed branch spine
   paths by their `d` string, which collides whenever two segments are
   degenerate — every segment before the cards are measured, and permanently
   under jsdom where every rect is zero. Keyed by position instead: these are
   positional geometry in a list that never reorders.

Also silenced the standing `react-hooks/incompatible-library` warning on
`useVirtualizer` with the rationale inline, so it stops being permanent noise
that would hide a real warning later.

Also de-raced `pagination.spec.ts`'s trailing-row assertion, which failed about
one run in three with "element(s) not found" — pre-existing, and unrelated to the
macOS fixes, but found while re-verifying them. The `show-more` trailer is
virtualized, so it is mounted only while the tail is in view, and each page that
lands grows the list *below* the current `scrollTop` and pushes it back out. The
test asserted once against whatever instant the first scroll happened to leave
behind; it now re-anchors to the tail on every polling attempt. Five consecutive
green runs after the change.

### Deploy readiness check: poll, don't sample (2026-08-30)

Reported from a real deploy: step 8's `systemctl --quiet is-active` failed, yet
`systemctl status layerlens.service` showed the service active when checked by
hand moments later. Two changes, because there are two things wrong.

**The check was a single sample of a value that is legitimately transient.** A
unit reports `activating` for as long as its `ExecStartPost` probe runs, and a
start that trips `TimeoutStartSec` lands in `failed` and is then picked straight
back up by `Restart=on-failure` — so one sample taken the instant `restart`
returns can fail a deploy whose service is healthy seconds later. It now polls
`ActiveState` (default 60 attempts, 1s apart, `LAYERLENS_DEPLOY_ACTIVE_RETRIES`),
settling on `active`, giving up immediately on `failed`, and printing
`systemctl status` plus 50 journal lines on the way out so a failed deploy says
why. Verified by executing the generated remote script against a stubbed
`systemctl`: active-immediately exits 0, activating-then-active exits 0 on the
fourth attempt (the reported case, previously an immediate exit 1), and failed
exits 1 without burning the full timeout.

`systemctl show -p ActiveState | cut` rather than `show --value`, because
`--value` needs systemd >= 230 and this script already has one lesson about
assuming a host tool is newer than the one installed.

**The unit could also cause the state it was being blamed for.** The readiness
probe was `curl --retry 30 --retry-delay 1 --max-time 10` with no bound on the
retry loop as a whole, so it could hold the unit in `activating (start-post)`
for minutes — past `TimeoutStartSec=120s`, at which point systemd aborts the
start, marks the unit `failed`, and `Restart=on-failure` revives it. That is
precisely "deploy said it failed, status says active". Adding
`--retry-max-time 60` bounds the worst case to ~70s inside the 120s budget.
`TestReadinessProbeFitsInsideTheStartTimeout` parses both values out of the unit
and fails if the probe can outlast the start budget; it fails against the
original unit.

### A failed restart must diagnose itself (2026-08-30)

A real deploy failed at step 8 with only systemd's one-line "Job for
layerlens.service failed because the control process exited with error code."
Under `set -eu` the remote script died on the `systemctl restart` line, so the
status and journal dumps below it never ran — the operator got a bare failure
for the one case that most needs evidence. Both failure paths (restart refused,
and never reaching `active`) now print `systemctl cat`, `systemctl status`, and
80 journal lines, asserted by `TestRestartStepDiagnosesBothFailurePaths` and
exercised against a stubbed `systemctl`.

`systemctl cat` is first deliberately: it prints the *effective* unit including
drop-ins. A leftover `/etc/systemd/system/<service>.service.d/` from an earlier
or unrelated deployment survives replacing the unit file and can inject
directives this repo never wrote — which is the leading explanation for a
"control process" failure against a unit whose only control process is the
`-`-prefixed `ExecStartPost`, whose failure systemd is required to ignore.

Context worth recording: the journal from that host showed a *different*
layerlens — `2026/08/28 ... INFO layerlens: source=demo listening on
http://[::]:8000`, stdlib `log` formatting rather than our `slog`, port 8000
rather than 8080, and whole `.tar` files written into
`/var/lib/layerlens/images`, which this implementation never does (it stores
zstd-compressed JSONL indexes). So the deploy is landing on a host that already
runs an unrelated service of the same name, sharing the unit name, the state
directory, and the data directory. `LAYERLENS_DEPLOY_SERVICE` already exists to
deploy under a different unit name.

Checked and ruled out locally: the server starts cleanly against a data
directory seeded with those foreign `.tar` files, serving `/healthz` as normal —
so stale state in `/var/lib/layerlens/images` is not the cause.

### Deployment service port (2026-08-30)

The deployed service now listens on **8000** by default, set with
`LAYERLENS_DEPLOY_SERVICE_PORT` and stamped into the unit's
`Environment=LAYERLENS_PORT` line at install time.

Made it a single source of truth rather than adding one more setting. The unit
previously carried `LAYERLENS_LISTEN=:8080` *and*
`LAYERLENS_HEALTH_URL=http://127.0.0.1:8080/healthz` with a comment reading
"Must agree with LAYERLENS_LISTEN" — a probe that can name a port the service is
not on, which would report a healthy deploy of an unreachable service. Both are
now derived from `LAYERLENS_PORT`: `--listen
${LAYERLENS_LISTEN_HOST}:${LAYERLENS_PORT}` in `ExecStart`, and the probe URL
inlined into `ExecStartPost`. `LAYERLENS_LISTEN_HOST` (empty = all interfaces)
preserves the ability to bind loopback only.

Three things worth recording:

- **The probe URL is inlined into `ExecStartPost` rather than kept as an
  `Environment=` value containing `${LAYERLENS_PORT}`.** systemd expands
  environment variables in `Exec*=` lines but *not* recursively inside other
  `Environment=` values, so the nested form would have been installed literally
  and the readiness gate would have polled a nonsense URL.
- **The deploy rewrites the unit's default rather than writing an
  `EnvironmentFile`.** The unit is deploy-owned and replaced on every deploy,
  while `/etc/layerlens/layerlens.env` is operator-owned and deliberately never
  touched. Because systemd reads `EnvironmentFile` *after* `Environment=`, an
  operator override still wins over whatever the last deploy stamped in — so the
  layering is: deploy sets the default, operator overrides it, and neither
  clobbers the other.
- **Ports below 1024 warn rather than fail.** The unit drops every capability, so
  the service cannot bind one — but an operator may have granted
  `AmbientCapabilities=CAP_NET_BIND_SERVICE` in their own override, and the
  deploy has no way to know.

The binary's own `--listen` default stays `:8080`, so a dev instance and a
deployed one do not collide on a machine running both.

### Filesystem tree and layer-panel UI revisions (2026-08-30)

Four changes requested after using the shipped UI.

1. **Size bars are scaled against the largest top-level entry, and the bar
   moved into the Size column.** They were normalized per sibling group, which
   re-stretched every directory to full width — a 3 KiB file inside a 4 MiB
   folder drew the same bar as the folder. One denominator for the whole
   visible tree makes "a child never out-draws its parent" arithmetic rather
   than something to remember, since a subtree's bytes are always a subset of
   its parent's. The denominator is the root page's `maxSiblingBytes`, which is
   already exactly "the largest entry at this tree's top level", so no new API
   surface was needed; the prop was renamed `maxSiblingBytes` → `scaleBytes`
   because the old name had become a lie. A unit test pins the ordering
   property. Merging the bar into the Size cell followed from the same
   observation: two columns were spending width on one quantity. The bar is
   absolutely positioned beneath the number rather than stacked with it — a
   stacked layout lifted "13.3 MiB" about 7px above "+4.8 MiB" beside it, and
   numbers that do not line up stop reading as a column.

2. **Columns reordered to `Name | ± | Size | Δ size | Files | Δ files`**, so
   each absolute is immediately followed by its own delta instead of the eye
   having to pair them across two columns.

3. **The count left the status column.** A directory read `± 66`, which was
   taken for a size or a file count as often as a descendant tally. The glyph
   alone still says "something below here changed", the Δ columns say how much,
   and the tally survives in the cell's tooltip and the row's SR sentence.

4. **The layer panel's selection chips are read-outs, not buttons.** They
   scrolled the diagram to the selected card — an affordance nothing about them
   advertised, on a diagram short enough that it was rarely wanted. They keep
   the job they were actually doing: naming which layer each side is pinned to.
   Their `cursor: pointer` and hover response went with the behavior, since
   DESIGN §1 makes those the signals that something is clickable.

5. **The chevron and the name are now two different verbs.** The chevron
   expands in place; clicking a directory's name re-roots the view onto it.
   Previously the name duplicated the chevron and a separate `↳` button did the
   re-rooting — an affordance that had to be explained. Each shape now does the
   thing its shape suggests: a triangle discloses, a name navigates.

