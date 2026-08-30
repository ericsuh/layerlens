# layerlens

Optimize your Docker images.

layerlens is a single Go binary that serves a JSON API plus an embedded React
SPA for comparing container images: their layer graphs (shared trunk, per-image
branches, could-be-shared layers) and the filesystem diff between any two
points in those graphs.

All project/agent planning files are in [.planning/](.planning/).

## Development

Toolchain and tasks are managed by [mise](https://mise.jdx.dev). Pinned tools
(Go, Node, golangci-lint, shellcheck) come from `mise.toml`; SPA dependencies
from `web/package.json`.

```sh
mise install        # provision Go, Node, golangci-lint, shellcheck
mise run build      # bundle the SPA and build ./bin/layerlens with it embedded
./bin/layerlens --listen :8080 --data-dir ./.dev-data
```

| Task | What it does |
|---|---|
| `mise run build` | esbuild + Tailwind → `internal/webui/dist`, then `go build -o bin/layerlens` |
| `mise run build-web` | Just the SPA bundle and stylesheet |
| `mise run build-linux` | The deploy artifact: `CGO_ENABLED=0 GOOS=linux GOARCH=amd64` → `bin/layerlens-linux-amd64` |
| `mise run bundle-size` | Bundle sizes from the esbuild metafile |
| `mise run dev` | esbuild + Tailwind watchers writing `.dev-dist`, plus the server run with `--ui-dir .dev-dist` |
| `mise run lint` | `golangci-lint run` (includes `go vet`) + eslint + shellcheck |
| `mise run typecheck` | `tsc --noEmit` |
| `mise run test` | `go test` + Vitest |
| `mise run check` | lint + typecheck + test + build |
| `mise run fmt` | `gofmt -w` over the Go trees |
| `mise run clean` | Remove build output, `web/node_modules`, and local runtime state — the tree as a fresh checkout |
| `mise run clean-deep` | `clean`, plus the shared Go build/test/module caches (forces a full redownload) |
| `mise run genfixtures` | Regenerate `fixtures/` from `cmd/genfixtures` (deterministic — expect no git diff) |
| `mise run e2e` | Playwright against the real binary on fixtures (no Docker, no network) |
| `mise run deploy-dry-run` | Print the deploy command plan; runs nothing, dials nothing |
| `mise run deploy` | Cross-compile, ship over SSH, restart the service, wait for `/healthz` |

`clean` returns the working tree to what a fresh `git clone` gives you: it
deletes only gitignored output, leaves tracked files alone (which is why
`internal/webui/dist` is emptied rather than removed — its `.gitkeep` is tracked
and `go:embed` needs the directory to exist), and then lists anything still
ignored that it did not delete, so the task and `.gitignore` cannot quietly
drift apart. From clean, `mise run check` rebuilds everything in about 17 s.

`clean-deep` additionally wipes the Go build, test, and module caches. Those are
not part of a checkout — they live outside the repo and are shared with every
other Go project on the machine — so they are a separate task rather than
something `clean` does behind your back. Use it to prove the build works from
genuinely nothing.

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

## Deployment

The deploy target is a Linux **amd64** server, supervised by systemd. Two
artifacts do the whole job: [`deploy/layerlens.service`](deploy/layerlens.service)
and [`deploy/deploy.sh`](deploy/deploy.sh).

```sh
export LAYERLENS_DEPLOY_HOST=layerlens.example.internal
mise run deploy-dry-run     # print the exact plan; runs nothing, dials nothing
mise run deploy             # cross-compile, ship, restart, wait for /healthz
```

`mise run deploy` cross-compiles `bin/layerlens-linux-amd64`
(`CGO_ENABLED=0 GOOS=linux GOARCH=amd64` — pure Go, so it links statically and
needs no libc on the target), then, over one SSH connection per step: creates
the install directory and the unprivileged `layerlens` system user, uploads the
binary, the `fixtures/` directory and the unit file, swaps them into place,
installs and reloads the unit, restarts the service, and finally polls
`/healthz` **on the server** until it answers `ok`.

### Configuration

Every knob is an environment variable; only the host is required, and it has no
default so that a bare run fails instead of guessing at a target.

| Variable | Default | What it is |
|---|---|---|
| `LAYERLENS_DEPLOY_HOST` | **required** | Target hostname or IP |
| `LAYERLENS_DEPLOY_USER` | `root` | SSH user; anything else uses `sudo -n` for the privileged steps |
| `LAYERLENS_DEPLOY_DIR` | `/opt/layerlens` | Remote install directory (binary + fixtures) |
| `LAYERLENS_DEPLOY_PORT` | `22` | SSH port |
| `LAYERLENS_DEPLOY_SERVICE` | `layerlens` | systemd unit name |
| `LAYERLENS_DEPLOY_BINARY` | `bin/layerlens-linux-amd64` | Local binary to ship |
| `LAYERLENS_DEPLOY_FIXTURES` | `fixtures` | Local fixtures directory to ship |
| `LAYERLENS_DEPLOY_UNIT` | `deploy/layerlens.service` | Local unit file to install |
| `LAYERLENS_DEPLOY_HEALTH_URL` | `http://127.0.0.1:8080/healthz` | Probed *on the remote host* after the restart |
| `LAYERLENS_DEPLOY_HEALTH_RETRIES` | `30` | Health poll attempts, 2s apart |
| `LAYERLENS_DEPLOY_ACTIVE_RETRIES` | `60` | Attempts to observe `ActiveState=active`, 1s apart |
| `LAYERLENS_DEPLOY_SUDO` | `sudo -n` unless user is root | Privilege prefix for the root-only steps |
| `LAYERLENS_DEPLOY_SSH_OPTS` | — | Extra `ssh`/`scp` options |
| `LAYERLENS_DEPLOY_DRY_RUN` | — | `1` prints the plan and executes nothing |

Runtime configuration belongs to the unit, not the deploy: it ships defaults
for `--listen`, `--data-dir`, `--fixtures-dir`, `--cache-max-bytes`,
`--docker-host`, and the resource guards below, and reads overrides from
`/etc/layerlens/layerlens.env`, which the deploy never touches.

### Resource guards

These bound what a single crafted or hostile image can cost the server. The
defaults are safe; they are surfaced as unit environment variables so an
operator can tune them without editing the unit.

| Flag | Default | What it bounds |
|---|---|---|
| `--max-layer-entries` | `2000000` | Distinct paths indexed per layer. A layer of many tiny entries is the cheapest way to amplify wire bytes into server memory — a crafted 14 MB blob reached 1.9 GB of heap before this cap existed. 4x the 500k-file design envelope. |
| `--max-concurrent-pulls` | `4` | In-flight pulls. Surplus submissions get `429 too_many_pulls`; resubmitting an already-running pull rejoins it and takes no slot. |
| `--pull-timeout` | `6h` | Backstop on a single pull; `0` disables. Generous on purpose — 6 h carries 25 GiB at ~1.2 MB/s. |
| `--docker-allow-tcp` | off | Required before `--docker-host` accepts a `tcp://` endpoint. The daemon path bypasses the guarded dialer by design, so opening it to TCP is opt-in. |

Registry bodies are additionally guarded by a throughput floor (4 KiB/s over a
30 s window) rather than a fixed deadline, so a slow-but-progressing large pull
is never killed while a trickling connection is.

### The dry run

`LAYERLENS_DEPLOY_DRY_RUN=1` (or `--dry-run`) prints every command the deploy
would run, verbatim and correctly shell-quoted, and executes none of them —
nothing is dialed and nothing is transferred. Local preflight still runs, so
the rehearsal also tells you whether the artifacts exist and whether the binary
is actually a linux/amd64 ELF; under dry-run a missing artifact is a warning
rather than a fatal error, so the whole plan still prints on an unbuilt tree.

Every remote command in the real deploy goes through one `run()` helper, which
is what makes the printed plan *the* command list rather than a narration of
it. `deploy/deploy_test.go` asserts the plan's contents and ordering with `ssh`
and `scp` replaced by stubs that fail loudly if they are ever executed.

### Deploy safety

- **Atomic binary swap.** The binary is uploaded to a staging path *inside*
  `LAYERLENS_DEPLOY_DIR` — the same filesystem as its destination — made
  root-owned and executable there, and then moved into place with a single
  `mv -f`, i.e. `rename(2)`. There is no instant at which the path systemd
  execs holds a partially transferred file, and renaming over a running
  executable replaces the directory entry while the live process keeps its own
  inode (overwriting it in place would instead fail with `ETXTBSY`).
- **Graceful restart.** `systemctl restart` sends one `SIGTERM`; the server
  stops accepting, drains in-flight requests, and exits. `TimeoutStopSec=30s`
  comfortably exceeds the server's own 15s drain budget, so systemd never
  `SIGKILL`s mid-drain. An in-flight pull is abandoned, but its committed layer
  indexes survive, so retrying it after the restart resumes rather than restarts.
- **Fixtures keep one previous copy** (`fixtures.previous`). A directory cannot
  be replaced by a single rename; the brief window where the new copy is being
  moved in is harmless because fixtures are read once at startup, and the
  restart happens afterwards.
- **Idempotent.** Re-running the deploy on a fresh host and on a
  deployed-to-a-hundred-times host does the same thing.

### The systemd unit

`Type=exec`, `Restart=on-failure`, journald logging under the `layerlens`
identifier, and an `ExecStartPost` `curl` that retries until `/healthz` stops
answering `503 loading` — so "started" means "the demo images are queryable",
not "the process was spawned".

It runs as a dedicated unprivileged `layerlens` user (created by the deploy)
under `ProtectSystem=strict` with **one** writable path: `StateDirectory=layerlens`
→ `/var/lib/layerlens`, which is where `--data-dir` lives. Also set:
`NoNewPrivileges`, `PrivateTmp`, `PrivateDevices`, `ProtectHome`,
`ProtectProc=invisible` + `ProcSubset=pid`, the `ProtectKernel*` /
`ProtectControlGroups` / `ProtectClock` / `ProtectHostname` set,
`RestrictNamespaces`, `RestrictRealtime`, `RestrictSUIDSGID`, `LockPersonality`,
`MemoryDenyWriteExecute` (pure Go, no JIT, so it costs nothing),
`SystemCallFilter=@system-service`, an **empty** `CapabilityBoundingSet` and
`AmbientCapabilities`, `RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX AF_NETLINK`,
and resource limits (`MemoryHigh=2G`, `MemoryMax=3G`, `TasksMax=512`,
`LimitNOFILE=8192`) sized against the ~1.5 GiB worst-case RSS in
ARCHITECTURE §4.6. `systemd-analyze security` rates it **1.5 "OK"**; the
remaining exposure is inherent to being a network server.

The **Docker daemon source is off by default in the unit**
(`LAYERLENS_DOCKER_HOST=off`), because reaching `/run/docker.sock` requires
`SupplementaryGroups=docker`, and membership in that group is equivalent to
root on the host. Both the group and the bind-mount are present in the unit,
commented out, for a host where that is already the trust model.

### Security posture, and what public exposure would need

**The deployed instance is assumed to be on a private / trusted network**
(RESEARCH Q6). There is no authentication layer, no per-user quota and no rate
limiting, and that is a deliberate scoping decision, not an oversight: the
registry allowlist and the guarded dialer exist to stop the *server* from being
used as an SSRF pivot, not to police users.

Before putting this on the public internet you would need, at minimum:

1. **Authentication** in front of everything (the API included, not just the SPA).
2. **Per-caller rate limiting on the pull endpoint.** `POST /api/v1/pulls` will
   happily fetch a 25 GiB image from a public registry: unauthenticated, that is
   a bandwidth and disk amplifier pointed at both your egress bill and your
   cache budget. `--max-concurrent-pulls` bounds the server globally, but
   nothing attributes cost to a caller, and any client can cancel any pull
   (`GET /api/v1/pulls` lists them all).
3. **A smaller `--cache-max-bytes`**, plus monitoring of it — the LRU refuses
   rather than thrashes, so a full cache degrades into `cache_full` errors.
4. **TLS**, terminated by a reverse proxy; the server speaks plain HTTP.

The cheap hardening in the unit is applied regardless of exposure, because it
is cheap and correct either way.

## Known limitations

- **linux/amd64 only.** Every path — registry, daemon and fixtures — resolves
  the `linux/amd64` variant and nothing else. A multiplatform index is not
  compared across its platforms; it is reduced to its amd64 member, and an
  image with no amd64 variant is refused with a clear error. Windows images
  are not supported at all. (PROJECT.md "Out of scope".)
- **Registry coverage is deep on two of five** (RESEARCH Q4). Docker Hub and
  GHCR are verified end to end against the live services. GCR, Artifact
  Registry, ACR and ECR Public are on the allowlist and go through the
  identical code path, but are **not** live-verified — expected to work, not
  guaranteed. ECR *private* has no anonymous access and cannot work by design.
- **Zstd layers are untested against a live registry** (ARCHITECTURE §10.9).
  Decompression is media-type-driven and gzip, zstd and uncompressed layers are
  all wired, but only gzip has been exercised end to end against a real upstream.
- **Anonymous pulls only** (RESEARCH Q3). No credential file, helper or cloud
  auth chain is consulted, so private repositories are unreachable — by design,
  since the alternative is a server that lends its own identity to any caller.
- **Pull state is in-memory** (ARCHITECTURE §10.4). A restart forgets in-flight
  pull IDs; the poller reports the pull as lost and offers a retry, which is
  cheap because the layer indexes committed before the restart are reused.
- **One process per cache directory**, enforced by an exclusive `flock`.
  Horizontal scaling is explicitly out of scope.
- **The cache cap bounds index bytes, not image bytes.** Layer blobs are never
  stored, so a 25 GiB image costs kilobytes of cache; `--cache-max-bytes` is a
  budget for the analysis metadata, and sizing it against image sizes will
  wildly over-provision.
- **No CI.** The gates are local (`mise run check`, `mise run e2e`).
