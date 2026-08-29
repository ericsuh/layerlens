# Phase 008 — Remote sources: registry pulls, Docker ingest, SSRF

## Goal

Add the two remote image sources and their trust boundary: allowlist-validated
anonymous registry pulls through the SSRF-hardened transport with real byte
progress, cancellation, and per-layer resume (the 25 GiB story), and
Docker-socket ingestion via a single streamed `docker save`. Ship the pulls
API, the Docker listing API, and the frontend's Registry and Docker tabs with
the phased progress UI. This completes all three PROJECT.md image sources.

## Scope

**In:** `internal/imgref` (parse + allowlist per §7.1, incl. RESEARCH Q10's
`*.pkg.dev`); `internal/safehttp` (guarded dialer, redirect cap, size limits
per §7.2); `internal/ingest` remote sources: registry (gcr `remote` with
`WithPlatform(linux/amd64)`, `WithTransport(safehttp)`, anonymous keychain
only per RESEARCH Q3), docker daemon (moby client: `ImageList`/`ImageInspect`
listing, one `ImageSave` stream parsed via gcr tarball/layout readers,
`RepoDigests` registry-preference optimization per DECISIONS A2), pull manager
(states, byte progress, in-flight dedupe/idempotency, cancel, staging cleanup,
skip already-indexed layers); server: `POST/GET/DELETE /api/v1/pulls*`,
`GET /api/v1/docker/images` (§6.2/§6.3); frontend: Registry tab (mono input,
inline pre-flight allowlist verdict, allowed-registries helper), Docker tab
(availability dot, rows, "will be analyzed" note), PullProgressList + slot
progress ring + phased progress card with ETA and Cancel (DESIGN §4.4),
error states #4–#15; e2e error-path tests + opt-in network smoke
(`E2E_NETWORK=1`) and opt-in docker test.

**Not in this phase:** credentials of any kind (RESEARCH Q3: anonymous only —
auth errors collapse to `pull_upstream_denied`); deep live verification of
GCR/ACR/ECR (RESEARCH Q4: allowlisted, same code path, documented limitation);
deploy.

## Prerequisites

Phases 005 (cachestore/API/ingest core) and 006 (shell, tabs). Phase 007 is
not a hard prerequisite but should normally land first so golden e2e guards
against regressions here.

## Files to create/modify

- `internal/imgref/imgref.go` + `imgref_test.go`
- `internal/safehttp/safehttp.go` + `safehttp_test.go`
- `internal/ingest/registry.go`, `docker.go`, `manager.go`, `progress.go`
  + tests
- `internal/server/handlers_pulls.go`, `handlers_docker.go` + tests
- `cmd/layerlens/main.go` — wire docker-host autodetect, safehttp transport
- `web/src/select/RegistryForm.tsx`, `DockerList.tsx`, `PullProgress.tsx`,
  `web/src/lib/refcheck.ts` (client-side mirror of allowlist verdict per
  DESIGN §10.7; server remains authoritative) + tests
- `e2e/errors.spec.ts`; `e2e/network.smoke.spec.ts` (skipped unless
  `E2E_NETWORK=1`); `e2e/docker.smoke.spec.ts` (skipped unless `E2E_DOCKER=1`)

## Implementation steps

1. `imgref` per §7.1: `name.ParseReference` → `RegistryStr()` → allowlist
   (exact: `index.docker.io`, `registry-1.docker.io`, `docker.io`, `ghcr.io`,
   `gcr.io`, `public.ecr.aws`; patterns on full-label boundaries: `*.gcr.io`,
   `*.pkg.dev`, `*.dkr.ecr.*.amazonaws.com`, `*.azurecr.io`); reject explicit
   ports and non-https; emit `domain.ImageRef` (gcr types stop here).
2. `safehttp` per §7.2: `guardedDial` (443 only; resolve; reject if ANY
   candidate IP is loopback/private/link-local/multicast/unspecified/ULA/
   IPv4-mapped; dial the vetted literal IP — no rebinding TOCTOU), redirect
   cap 10 + no-downgrade `CheckRedirect`, `io.LimitReader` (8 MiB) on
   manifests/configs, layer-count cap 512. Injectable resolver for tests.
3. Registry source: resolve manifest (platform selection skips attestation
   manifests), compute `bytesTotal` from manifest layer sizes, stream each
   not-yet-indexed layer through the phase-002 indexer into staged commits;
   progress via counting reader; context cancellation deletes staging, keeps
   committed layers (§4.1 checkpoint semantics).
4. Docker source: socket autodetect (`--docker-host`/`DOCKER_HOST`/default);
   `ListDockerImages` via ImageList+Inspect (cheap, no transfer,
   `alreadyAnalyzed` cross-ref); ingest = prefer registry path when an
   allowlisted RepoDigest exists, else one `ImageSave` call with platform
   selection, streamed to a staging spool and parsed with gcr layout/tarball
   readers (Engine 29 OCI-layout saves and legacy saves both — DECISIONS A2);
   progress flagged `bytesEstimated: true`.
5. Pull manager per §6.3: id allocation, state machine
   (resolving→running→done/error/cancelled), idempotent POST (in-flight or
   already-cached returns 200 with existing status), `DELETE` cancel; statuses
   in-memory (restart loses them — §10.4 accepted).
6. Server endpoints per §6.3 exactly, incl. the non-leaky
   `pull_upstream_denied` collapse of 401/403/404 and `docker_unavailable`
   only on endpoints that need the socket; `/docker/images` never errors for
   "no docker" (`available:false` + reason).
7. Frontend: Registry tab with per-keystroke local verdict
   ("→ ghcr.io ✓ allowed" / not-allowed with the allowed-list, DESIGN §4.3
   pattern #7/#8); phased progress card (§4.4: resolving → determinate
   download+index with bytes/layers/throughput/soft ETA → finalizing),
   per-layer detail collapse, Cancel semantics (state #15), slot progress
   ring, tab-switch persistence; Docker tab per DESIGN §4.3; `['pulls']`/
   `['pull', id]` polling policies per §8.2 (invalidate `['images']` on done).
8. E2e: error paths (`not a ref!` inline parse error;
   `evil.example.com/x` allowlist message; states #7/#8); network smoke
   (Hub + GHCR pull incl. progress and cancel-mid-pull, opt-in per RESEARCH
   Q4); docker smoke opt-in.
9. Gates; status update; commit.

## Test cases

`imgref_test.go` (§9.1 slice — exhaustive table):
- accepts: `alpine:3.20` (Hub shorthand → `index.docker.io/library/alpine`),
  `ghcr.io/org/img:tag`, `gcr.io/p/i`, `us.gcr.io/p/i`,
  `us-docker.pkg.dev/p/r/i`, `public.ecr.aws/x/y`,
  `123456789012.dkr.ecr.us-east-1.amazonaws.com/repo`, `foo.azurecr.io/bar`,
  digest references.
- rejects: `evilgcr.io/x` (substring attack), `x.azurecr.io.evil.com/y`
  (suffix attack), `registry.example.com/x`, `ghcr.io:8443/x` (explicit
  port), `localhost/x`, `127.0.0.1/x`, empty/garbage (`not a ref!`).

`safehttp_test.go` (fake resolver + local test servers):
- `rejects_loopback_private_linklocal_ula_multicast_unspecified` (table over
  IPs incl. `::1`, `10.x`, `169.254.x`, `fc00::`, `ff02::`).
- `rejects_ipv4_mapped_ipv6` (`::ffff:10.0.0.1` unwrapped and re-checked).
- `rejects_mixed_public_private_dns_answers` (ANY private ⇒ fail).
- `dials_vetted_literal_ip_no_second_resolution`.
- `redirect_cap_10`; `redirect_http_downgrade_rejected`.
- `non_443_port_rejected`; `manifest_size_limit_enforced`.

`ingest` (`manager_test.go`, `registry_test.go` with an httptest fake
registry serving fixture blobs; `docker_test.go` with a fake save stream):
- `pull_progress_bytes_exact_from_manifest`; `layers_skipped_already_indexed`
  (pull v2 after v1 → trunk layers skipped, `layersSkipped` counted).
- `cancel_mid_layer_cleans_staging_keeps_committed`; retry after cancel
  resumes at layer granularity.
- `idempotent_duplicate_post_returns_existing`; `done_sets_imageId`.
- `upstream_401_403_404_collapse_to_pull_upstream_denied` (non-leaky).
- `docker_listing_unavailable_shape` (no socket → `available:false`, no error).
- `docker_save_stream_parsed_oci_and_legacy` (two fake save shapes).
- `repo_digest_prefers_registry_path`.
- `cache_full_during_pull_aborts_with_507` (tiny cap; phase-005 refusal path
  exercised via the pull manager).

Frontend (Vitest):
- `refcheck_mirror_matches_server_table` (same accept/reject table as
  `imgref_test.go`, kept in one shared JSON test-vector file so the two
  implementations cannot drift).
- `pull_polling_state_machine`: §9.3 case — polling stops on terminal states;
  `done` invalidates `['images']` (mocked fetch).
- `progress_card_phases_render`; `cancel_flow_restores_panel`.

Playwright:
- `errors.spec.ts`: parse error inline; allowlist rejection message lists the
  allowed registries; no network occurs (assert no request leaves via route
  interception).
- `network.smoke.spec.ts` (opt-in): small Hub + GHCR image pull end-to-end
  with visible progress states and cancel-mid-pull.

## Acceptance criteria

- All three PROJECT.md image sources work: cached list (phase 5), Docker
  daemon (listing + ingest with progress when a socket exists; section absent
  without one, nothing errors — UAT §9.5 item 11), registry input with
  pre-flight verdict and phased progress (UAT item 12).
- SSRF: the allowlist gates `POST /pulls` before any network I/O; every
  outbound socket (including CDN redirect hops) passes the guarded dialer; the
  full `imgref`/`safehttp` §9.1 test tables pass.
- 25 GiB readiness is structural and observable: no code path buffers a layer
  or the save stream in memory (review + `daemon.WithUnbufferedOpener`-
  equivalent single `ImageSave` call); progress reports true byte counts;
  cancel + resume works at layer granularity (automated test + UAT item 12).
- Anonymous-only: no credential reading anywhere; private images yield the
  §6.1 `pull_upstream_denied` message verbatim from DESIGN state #10.
- `mise run test` and `mise run e2e` stay green with no docker and no network;
  the opt-in suites pass when their flags and prerequisites are present.
- Known limitation documented (RESEARCH Q4): GCR/ACR/ECR allowlisted and
  code-identical but not live-verified — noted in README.

## Risks / gotchas

- **The dialer is the security boundary** — the redirect chain must never
  bypass it. Give gcr a plain `*http.Transport` via `remote.WithTransport`
  (not a `transport.Wrapper`), and keep the redirect policy on the client gcr
  actually uses; verify with a test server that 302s to a loopback URL (must
  fail at dial).
- Docker Hub rate limits (DECISIONS risk 6): the network smoke test should
  pull a tiny image and tolerate 429 by skipping-with-message, not failing
  the suite.
- `daemon` saves of multi-platform images include all platforms under the
  containerd store — pass explicit platform to ImageSave (DECISIONS A2
  gotcha) and skip `unknown/unknown` attestation manifests when indexing the
  save.
- The save stream is sequential: skipping known layers still requires
  draining bytes — drain cheaply (no hashing) rather than re-hashing
  (DECISIONS A2), and prefer the registry path when RepoDigests allows.
- Progress UI churn: throttle status updates server-side (e.g. 100 ms) so
  25 GiB pulls don't melt the poller; the client polls at 500–1000 ms (§6.3).
- exe.dev deployment is trusted-network only (RESEARCH Q6) — do NOT add auth
  or quotas here; just keep the README caveat for later exposure.
