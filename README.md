# layerlens

Optimize your Docker images.

layerlens is a single Go binary that serves a JSON API plus an embedded React
SPA for comparing container images: their layer graphs (shared trunk, per-image
branches, could-be-shared layers) and the filesystem diff between any two
points in those graphs.

All project/agent planning files are in [.planning/](.planning/).

## Development

Toolchain and tasks are managed by [mise](https://mise.jdx.dev). Pinned tools
(Go, Node, golangci-lint) come from `mise.toml`; SPA dependencies from
`web/package.json`.

```sh
mise install        # provision Go, Node, golangci-lint
mise run build      # bundle the SPA and build ./bin/layerlens with it embedded
./bin/layerlens --listen :8080 --data-dir ./.dev-data
```

| Task | What it does |
|---|---|
| `mise run build` | esbuild + Tailwind → `internal/webui/dist`, then `go build -o bin/layerlens` |
| `mise run build-web` | Just the SPA bundle and stylesheet |
| `mise run bundle-size` | Bundle sizes from the esbuild metafile |
| `mise run dev` | esbuild + Tailwind watchers writing `.dev-dist`, plus the server run with `--ui-dir .dev-dist` |
| `mise run lint` | `golangci-lint run` (includes `go vet`) + eslint |
| `mise run typecheck` | `tsc --noEmit` |
| `mise run test` | `go test` + Vitest |
| `mise run check` | lint + typecheck + test + build |
| `mise run fmt` | `gofmt -w` over the Go trees |
| `mise run genfixtures` | Regenerate `fixtures/` from `cmd/genfixtures` (deterministic — expect no git diff) |

The built SPA lives in `internal/webui/dist` and is embedded with `//go:embed`,
so the binary is self-contained: it serves `/healthz`, the reserved `/api`
namespace, and the SPA (with fallback for client-side routes) from one process.

`internal/webui/dist` is a build-only artifact: only `mise run build-web` ever
writes it. `mise run dev` builds into `.dev-dist` instead, so a dev session can
never leave an unminified bundle behind for the next `mise run build` to embed.

## HTTP API

Everything under `/api/v1` answers JSON, including its failures: an error is
always `{"error":{"code","message","details?"}}` with a stable machine-readable
code, and the reserved `/api` namespace never falls through to the SPA shell.

| Endpoint | What it returns |
|---|---|
| `GET /healthz` | `ok` once the fixtures are analyzed; `503 loading` before that |
| `GET /api/v1/images` | Every analyzed image: refs, source, layer count, size, pinned |
| `GET /api/v1/images/{id}` | One image plus its layers (DiffID, ChainID, instruction, size) |
| `GET /api/v1/diff/layers?left=&right=` | The layer graph: shared trunk, both branches, could-be-shared edges |
| `GET /api/v1/diff/tree?left=&right=&leftLayers=&rightLayers=&path=` | One directory of the unified diff tree, server-aggregated and paginated |
| `GET /api/v1/meta` | Version, the registry allowlist, and cache usage against `--cache-max-bytes` |
| `GET /api/v1/docker/images` | Local daemon images; `available:false` with a reason when there is no socket — never an error |
| `POST /api/v1/pulls` | Start (or join) an analysis of a registry or daemon image: `{"source":"registry"\|"docker","reference":"…"}` |
| `GET /api/v1/pulls` · `GET /api/v1/pulls/{id}` | Pull status with byte-accurate progress |
| `DELETE /api/v1/pulls/{id}` | Cancel a pull; committed layer indexes are kept, so a retry resumes |

`id` is always the image's config digest (`sha256:…`), which is what `left`,
`right` and the `/images/{id}` path all take. `leftLayers`/`rightLayers` are
**counts**, not indexes: `6` means "the filesystem after layer 6", and `0` is
the empty filesystem before any layer.

The tree endpoint expands one directory at a time and never serializes a whole
tree: `depth` (1 or 2), `limit` (≤ 1000) and an opaque `cursor` bound the
payload, and `filter=changed` prunes unchanged subtrees. Every row carries both
sides' subtree byte totals and file counts, so the client formats and never
computes. A cursor is valid only for the exact query that issued it; anything
else is `bad_request` and the client refetches from page 1.

### The golden workflow, by hand

With no network and no Docker:

```sh
./bin/layerlens --listen 127.0.0.1:8080 --data-dir ./.dev-data --fixtures-dir fixtures &
curl -s localhost:8080/healthz                       # -> ok

# The two demo images, by tag
L=$(curl -s localhost:8080/api/v1/images | jq -r '.images[]|select(.refNames[0]=="example:v1").id')
R=$(curl -s localhost:8080/api/v1/images | jq -r '.images[]|select(.refNames[0]=="example:v2").id')

# Layer graph: 5 shared layers, then a fork, and two dotted edges
curl -s "localhost:8080/api/v1/diff/layers?left=$L&right=$R" | jq '{trunkLength, couldBeShared}'
# -> trunkLength 5; edge 6<->6 diffIdEqual:false (same files, different mtimes)
#                   edge 7<->7 diffIdEqual:true  (byte-identical tar)

# Filesystem diff at the fork: the .dockerignore mistake
curl -s "localhost:8080/api/v1/diff/tree?left=$L&right=$R&leftLayers=6&rightLayers=6&path=/app&filter=changed" \
  | jq '.rows[]|{name,status}'
# -> .git added, src modified, .env added, debug.log added, main.js modified
```

`cmd/layerlens/e2e_test.go` runs exactly this sequence against a real listener.

## Remote sources and the trust boundary

layerlens analyzes images from three places: the vendored fixtures, the local
Docker daemon, and a public registry. The registry path is where user input
reaches the network, so it has two independent controls.

**The allowlist** decides which registry a user may *name*. It is matched on
whole dot-separated labels, so `*.gcr.io` accepts `us.gcr.io` and rejects
`evilgcr.io`; `*.azurecr.io` rejects `x.azurecr.io.evil.com`. Explicit ports
and anything that would be fetched over plain http are refused. The check runs
synchronously in `POST /api/v1/pulls`, before a socket is opened, and the list
is served at `/api/v1/meta` so the UI never carries a second, drifting copy:

    docker.io · index.docker.io · registry-1.docker.io · ghcr.io
    gcr.io · *.gcr.io · *.pkg.dev · public.ecr.aws
    *.dkr.ecr.*.amazonaws.com · *.azurecr.io

**The guarded dialer** decides which address the process may *connect to*, on
every socket it opens. An allowlist cannot cover this on its own: a blob GET
legitimately redirects to a CDN whose hostname nobody can enumerate. So every
connection — the token endpoint, the manifest, each blob, and each redirect hop
— resolves once, refuses the host outright if *any* answer is loopback,
private, link-local, multicast, unspecified, unique-local, IPv4-mapped or a
v4-embedding v6 form, and then dials the vetted literal address, so there is no
window for DNS rebinding between the check and the connection. Plaintext cannot
be dialed at all, which is what makes an https→http downgrade impossible even
through somebody else's redirect handling. Manifests, indexes, configs and
token responses are size-capped, and the layer count is capped, so a
compromised-but-allowlisted upstream cannot exhaust memory.

**Pulls are anonymous, always.** No `~/.docker/config.json`, no credential
helpers, no cloud SDK auth chain — nothing on this machine can be used to reach
an image the public cannot. A private repository therefore answers with one
deliberately indistinguishable outcome for 401, 403 and 404 ("not found, or it
requires authentication"), because an anonymous puller that distinguished them
would be a probe for the existence of private repositories.

**Known limitation (RESEARCH Q4):** Docker Hub and GHCR are verified end to end
against the live services. GCR, Artifact Registry, ACR and ECR Public are on
the allowlist and go through the identical code path, but are **not**
live-verified — treat them as expected-to-work, not guaranteed. (ECR *private*
has no anonymous access at all and cannot work by design.) The opt-in suites
that hit the real world are off by default so `mise run test` stays hermetic:

    LAYERLENS_NETWORK_TESTS=1 go test ./internal/ingest/ -run TestLive
    LAYERLENS_DOCKER_TESTS=1  go test ./internal/ingest/ -run TestLiveDocker
    E2E_NETWORK=1 mise run e2e     # the Playwright network smoke test
    E2E_DOCKER=1  mise run e2e     # the Playwright docker smoke test

**The Docker daemon path** reads one `docker save` stream, once, for the
explicit `linux/amd64` platform, and indexes it as it arrives — no spool file,
and nothing held in memory but the stream's metadata members (the manifests and
the config, bounded at 64 MiB), which is what makes a 25 GiB local image viable
on a server whose disk budget is the analysis cache. When the local image carries a
digest for an allowlisted registry, the registry path is preferred instead: the
bytes are identical, and that route can skip layers already indexed rather than
draining them out of a sequential stream. A server with no socket reports the
daemon source as unavailable; nothing errors. `--docker-host off` turns the
source off explicitly on a host that does have one.

## Analysis cache

`--data-dir` holds one metadata index per layer, keyed by DiffID, plus one
record per analyzed image. Layer blobs and extracted filesystems are never
stored: a layer is streamed once, hashed, and reduced to its changeset, so the
ten demo images cost ~190 KB on disk.

- **Content-addressed.** Two images sharing a base share its indexes, and
  analyzing the second one skips those layers without reading a byte.
- **Crash-safe.** Files are staged, fsync'd and renamed into place; a layer
  directory is committed index-first and sidecar-last, and an image record only
  after all of its layers. Whatever a crash leaves behind is swept at the next
  start.
- **Single-writer.** An exclusive `flock` on the data directory is taken at
  startup and held for the process lifetime; a second server on the same
  directory fails immediately with a clear message.
- **Bounded.** `--cache-max-bytes` (default 50 GiB) caps it. Over the cap,
  un-pinned images are evicted least-recently-used first; an image that cannot
  fit even with everything evictable gone is refused with `cache_full` rather
  than thrash-evicting the cache on its behalf. The vendored fixtures are
  pinned and never evicted.

## Demo fixtures

`fixtures/` holds five vendored OCI image layouts — one directory per
comparable pair — that the server loads at startup, so the demo and the
end-to-end tests need neither Docker nor the network (RESEARCH Q2).

| Layout | Images | What it demonstrates |
|---|---|---|
| `fixtures/example` | `example:v1`, `example:v2` | The golden demo: a shared node-base trunk, a `COPY . .` layer that forks because a missing `.dockerignore` let `.git/`, `debug.log` and `.env` into the build context, and the content-identical `npm install` layer that fork invalidated |
| `fixtures/prefix` | `prefix:base`, `prefix:extended` | One image is a strict prefix of the other: one branch is empty |
| `fixtures/disjoint` | `disjoint:a`, `disjoint:b` | No shared layers at all |
| `fixtures/edgecase` | `edgecase:opaque`, `edgecase:plain` | Opaque directories, dir↔file type changes, symlink retargets, dangling hardlinks, devices, xattrs, and a mode-only change |
| `fixtures/wide` | `wide:v1`, `wide:v2` | A directory with 2,500 children, for tree pagination |

They are generated, not captured: every byte comes from Go literals in
`cmd/genfixtures/gen`, and `mise run genfixtures` reproduces the whole tree
byte for byte (227 KiB on disk for ~180 MiB of nominal image content — file
bodies are seeded banners followed by NUL padding). The tests in
`cmd/genfixtures/gen` re-derive the fixtures and diff them against what is
committed, so a stale `fixtures/` fails `mise run test`.
