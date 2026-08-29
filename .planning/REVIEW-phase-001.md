# Review — Phase 001 (commit `8e98bb3`)

Independent review, report-only. Verified by running `mise run check`, a clean
clone + build, and ~40 adversarial requests against the built binary. All five
phase-001 acceptance criteria pass. Traversal battery (12 encodings) found no
escape from the embedded FS. Package layout and error envelope match
ARCHITECTURE §2/§6.1 exactly; all pinned versions match.

Status: **fixes pending** — applied after phase 002 lands, to avoid editing
`mise.toml` underneath a running agent's verification.

## Must fix (build pipeline — one root cause)

`mise run dev` and `mise run build` share the output directory
`internal/webui/dist`, which `go:embed` reads at compile time. Three defects
fall out of that single mistake:

- **M1 — `mise run build` ships the dev bundle.** `build-web` declares
  `sources`/`outputs`; after `dev` has run, the outputs are present and newer,
  so the task skips and `go build` embeds the *unminified* 1.19 MB bundle plus
  a 1.83 MB source map. Demonstrated: binary 8.63 MB → 11.45 MB, and the
  "production" binary serves `GET /app.js.map` → 200, 1,826,110 bytes. This
  contradicts the delta recorded in DECISIONS.md claiming production omits the
  map. **Wrong release artifact, silently.**
- **M2 — `mise run dev` races `go:embed` and crashes.** The esbuild watcher
  rewrites the directory `go run` is embedding: `copy internal/webui/dist/app.js:
  unexpected length 0 != 1186955`. Reproduced 1 in 4 cold starts. Compounded by
  the `trap` being installed *after* the watchers are backgrounded, and by
  `kill 0` signalling mise's own process group — a watcher survived the failure.
- **M3 — `test-go` races `build-web` inside `mise run check`.** `test-go`
  declares no `depends`, so it compiles while `build-web` deletes and rewrites
  `dist`. Benign only because no Go test reads real bundle bytes yet; becomes a
  flake in phase 007.

**Single fix for all three:** give `dev` its own output directory
(`.dev-dist`) and run it with `--ui-dir .dev-dist`, leaving
`internal/webui/dist` a build-only artifact. Also set the trap before launching
watchers and kill saved PIDs rather than `kill 0`; add `depends` to `test-go`.

## Should fix (foundation gaps — cheaper now than in every later phase)

- **m12 — no panic recovery and no request logging.** §6.1 defines `internal`/500
  but nothing can produce it; the injected logger is used once. Every later phase
  would reinvent this. Add recovery → 500 envelope + an access log now.
- **m1 — the asset handler accepts every method.** `PUT /app.js` → 200 with the
  asset; `DELETE /` → 200 with the shell. `/healthz` correctly 405s; assets don't.
- **m2 — a missing asset returns `200 text/html`.** `GET /nonexistent.js` serves
  the SPA shell, so a browser parses HTML as JavaScript and a renamed-bundle
  regression fails silently.
- **m3 — no security headers.** No `nosniff`, CSP, or `Referrer-Policy`. With m2
  live, content sniffing is a real concern rather than theoretical.
- **m5 — `/api` can still return HTML.** The mux redirect bypasses the envelope:
  `GET /api` and `GET /api/v1//images` → 307 with an HTML body. Breaks §6.1's
  promise that the reserved namespace never falls through to HTML.
- **m6 — 405 returns `code: "bad_request"`**, which §6.1 pins to HTTP 400. Add a
  `method_not_allowed` row to §6.1 and record the delta.
- **m7 — unbounded path reflection.** A 60 KB URL yields a 60 KB JSON body. It is
  JSON-escaped and inert, but it is attacker-controlled echo and it will be logged.
- **m8 — `DOCKER_HOST` leaks into `-h`.** The flag default is the live env value,
  which can carry a host and credentials. Resolve the env after `fs.Parse`. The
  §1.3 `/var/run/docker.sock` autodetect is also still missing.

## Nice to have

- **m4 — assets have no cache validators.** `embed.FS` has a zero ModTime, so no
  `Last-Modified`, no `ETag`, no `Cache-Control`; every reload refetches 220 KB
  and can never 304.
- **m9/m10/m11/m13** — `--listen` and `--data-dir` unvalidated (`--listen ""`
  silently means `:80`); `--ui-dir` doesn't fail fast on a bad path; `GET
  /healthz/` returns the SPA shell with 200 (a mistyped health URL would "pass"
  against HTML); no `IdleTimeout`/`MaxHeaderBytes`, and a second SIGINT during
  the drain is swallowed.
- **m14 — bare `go test ./...` still traverses `web/node_modules`.** A nested
  one-line `web/go.mod` stops it at a module boundary durably.
- **m15 — `formatBytes` has a dead branch** (`value < 10 ? 1 : value < 100 ? 1 : 0`
  reduces to `value < 100 ? 1 : 0`) and untested boundaries: `formatBytes(1048575)`
  → `"1024 KiB"`, never promoted to MiB. DESIGN's own `1.02 GiB` example
  contradicts its stated rule — resolve before phase 007 forks a second formatter.

## Test gaps worth closing

- `run()` / graceful shutdown has **zero tests** — the riskiest concurrency here.
- `webui.Handler()` (the real embed) is never exercised; only `HandlerFS` over a
  `fstest.MapFS`.
- **The built bundle is never executed in a test.** `App.test.tsx` renders from
  source, so the stated reason for choosing an IIFE build is unverified and the
  "JS actually executes" criterion rests on a manual check. A ~15-line Vitest that
  evals the built bundle into jsdom would close a whole class of build-flag
  regressions.
- `TestEmbeddedFS` is a tautology: `assert.NotNil(entries)` cannot fail when
  `err == nil`. Assert a named entry.

## Scope notes

- **Overreach:** `web/src/lib/format.ts` implements DESIGN's full size rule; the
  phase asked only for a smoke test and the matrix assigns this to phase 007.
  Phase 007 must extend it, not fork it.
- **Deferred without a recorded delta:** §1.3's docker-socket autodetect, and env
  support for anything but `DOCKER_HOST`.
