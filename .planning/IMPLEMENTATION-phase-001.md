# Phase 001 — Toolchain & walking skeleton

## Goal

Stand up the entire toolchain and a minimal end-to-end skeleton: a mise-managed
repo where one command builds a single Go binary that embeds an esbuild-bundled
React/Tailwind page via `//go:embed`, serves it plus a `/healthz` endpoint, and
where lint, typecheck, and both unit-test runners are wired and green. Every
later phase inherits these gates unchanged; this phase proves the whole
build-and-embed pipeline before any product code exists.

## Scope

**In:** mise tools + tasks; Go module + `cmd/layerlens` entrypoint with flag
parsing and graceful shutdown (ARCHITECTURE §1.3 flags declared, mostly unused
yet); `internal/server` with `/healthz` and routing skeleton; `internal/webui`
embed + SPA-fallback file server; `web/` scaffold (React 19, TanStack Query
installed, Tailwind v4 CLI, esbuild bundle, tsc, eslint, Vitest + Testing
Library); golangci-lint v2 config; `.gitignore`; one smoke test per language.

**Not in this phase:** any domain/analysis code; any real API endpoint beyond
`/healthz`; shadcn components beyond what the skeleton page needs; Playwright
(phase 007); fixtures; Docker/registry access; deploy tasks.

## Prerequisites

None. Environment note: `mise` is installed in the dev sandbox via npm; Go
1.26, Node 22, and Docker are already present.

## Files to create/modify

- `mise.toml` — `[tools]`: `go = "1.26"`, `node = "22"`,
  `golangci-lint = "2.13.2"`; `[tasks.*]` below.
- `go.mod`, `go.sum` — module `github.com/ericsuh/layerlens` (or similar), Go 1.26.
- `cmd/layerlens/main.go` — flags per ARCHITECTURE §1.3 (`--listen`,
  `--data-dir`, `--cache-max-bytes`, `--fixtures-dir`, `--docker-host`; unused
  ones plumbed but inert), http.Server with graceful shutdown.
- `internal/server/server.go`, `internal/server/server_test.go` — mux, `GET /healthz`
  → `200 ok` (fixture gating arrives in phase 005), fallthrough to webui.
- `internal/webui/webui.go` — `//go:embed all:dist`, SPA fallback (unknown
  non-`/api` paths serve `index.html`).
- `internal/webui/dist/.gitkeep` — embed target exists in a clean checkout;
  `dist/*` gitignored except `.gitkeep`.
- `web/package.json`, `web/tsconfig.json` (TS 5.9.3, strict), `web/eslint.config.js`
  (eslint 10 + typescript-eslint 8), `web/vitest.config.ts` (jsdom),
  `web/src/main.tsx`, `web/src/App.tsx` (placeholder page), `web/src/app.css`
  (Tailwind v4 `@theme` with the DESIGN §2.4 tokens declared), `web/index.html`
  template copied into dist by the build task.
- `web/src/lib/format.test.ts` — placeholder smoke (real formatting tests in 006).
- `.golangci.yml` — v2 schema; default linters (includes vet) + `gofmt`.
- `.gitignore` — `bin/`, `internal/webui/dist/*`, `web/node_modules/`, `.e2e-data/`.

## Implementation steps

1. Write `mise.toml` with tools and tasks (DECISIONS §D syntax, verified):
   - `build-web`: esbuild bundle (`--bundle --minify --metafile`) +
     `@tailwindcss/cli -i web/src/app.css -o internal/webui/dist/app.css` +
     copy `index.html`; `sources`/`outputs` for caching.
   - `build`: `depends = ["build-web"]`; `go build -o bin/layerlens ./cmd/layerlens`.
   - `test-go`: `go test ./...`; `test-web`: `vitest run`; `test`: depends on both.
   - `lint`: `golangci-lint run` + `eslint`; `typecheck`: `tsc --noEmit`;
   - `dev`: esbuild `--watch` + tailwind `--watch` + `go run` (document as three
     processes or use mise parallel deps).
2. `go mod init`; write `main.go` (flags, signal-aware shutdown), `server`
   (mux + healthz), `webui` (embed + fallback). Keep `/api/` reserved: any
   unknown `/api/*` path returns a 404 JSON body in the ARCHITECTURE §6.1
   error-envelope shape (never the SPA fallback). Define the envelope type
   here so every later phase reuses it.
3. Scaffold `web/` with pinned versions from DECISIONS (react 19.2.8,
   @tanstack/react-query 5.102.8, typescript 5.9.3, esbuild 0.28.2, tailwindcss
   4.3.3, vitest 4.1.11). Placeholder `<App/>` renders "layerlens" + the theme
   tokens applied, so the embed path is visibly proven.
4. Wire golangci-lint config; run all tasks; fix until green.
5. Commit.

## Test cases

- `TestHealthz` (Go, httptest): `GET /healthz` → 200, body `ok`, `text/plain`.
- `TestSPAFallback` (Go): `GET /some/client/route` → 200 with `index.html`
  content; `GET /api/v1/nonexistent` → JSON error envelope, not HTML.
- `TestErrorEnvelopeShape` (Go): envelope marshals exactly per §6.1
  (`error.code`, `error.message`, optional `details`).
- `format.test.ts` (Vitest smoke): trivial passing test proving the runner +
  jsdom environment work.

## Acceptance criteria

- Fresh clone → `mise install && mise run build` produces `bin/layerlens` with
  no network beyond module/npm downloads.
- `./bin/layerlens --listen :8080` serves: `/healthz` → `ok`; `/` → the bundled
  React page (verify the JS actually executes: page shows a React-rendered
  string, not static HTML); a deep link like `/compare` → same page (SPA fallback).
- The served assets come from the embedded FS: deleting `internal/webui/dist`
  *after* building the binary does not change what the binary serves.
- `mise run lint`, `mise run typecheck`, `mise run test` all exit 0.
- `esbuild --metafile` output exists so bundle size is observable from now on.

## Risks / gotchas

- `go:embed` fails on empty/missing dirs — the `.gitkeep` + `all:dist` pattern
  and task ordering (`build` depends on `build-web`) must make clean-checkout
  builds deterministic.
- Tailwind v4 has no esbuild plugin path (DECISIONS §C5) — it is a parallel CLI
  step; don't let anyone "simplify" it into an esbuild plugin.
- mise in this sandbox is npm-installed; tasks must not assume a mise-managed
  Go/Node if the pinned versions conflict with preinstalled ones — verify
  `mise x -- go version` matches 1.26 early.
- Keep `web/` out of the Go module's package space (no `.go` files) so
  `go test ./...` doesn't traverse node_modules.
