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

## Known limitations

- **linux/amd64 only.** Every path — registry, daemon and fixtures — resolves
  the `linux/amd64` variant and nothing else. A multiplatform index is not
  compared across its platforms; it is reduced to its amd64 member, and an
  image with no amd64 variant is refused with a clear error. Windows images
  are not supported at all.
- **Anonymous pulls only** No credential file, helper or cloud
  auth chain is consulted, so private repositories are unreachable — by design,
  since the alternative is a server that lends its own identity to any caller.
- **Pull state is in-memory** A restart forgets in-flight
  pull IDs; the poller reports the pull as lost and offers a retry, which is
  cheap because the layer indexes committed before the restart are reused.
- **One process per cache directory**, enforced by an exclusive `flock`.
  Horizontal scaling is explicitly out of scope.
- **No CI.** The gates are local (`mise run check`, `mise run e2e`).
