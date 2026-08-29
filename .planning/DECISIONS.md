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
