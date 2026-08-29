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
