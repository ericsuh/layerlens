# Security review — Phase 008 (commit `c05082c`)

Adversarial, report-only. Attack code ran via `go test -overlay` from a
scratchpad; the repo was untouched. Gates: `go vet`, `go test`, `go test -race`,
`golangci-lint run ./...` (0 issues), plus both opt-in live suites.

**Verdict: safe on a trusted network as specified. Not safe if exposed publicly
— and the reason is availability, not the SSRF story.**

## The SSRF control holds — nothing got through

Attempted and **reproduced-blocked**, against TLS servers impersonating
allowlisted hosts:

| Attack | Result |
|---|---|
| Blob 302 → `169.254.169.254`, followed by **go-containerregistry's own client**, where our `CheckRedirect` never runs | refused at `dialTLS`; stand-in metadata service recorded **0 hits** |
| `WWW-Authenticate` pointing the token fetch at a private address | refused before the token request; 0 hits |
| `HTTP_PROXY`/`HTTPS_PROXY` set — control run confirmed stdlib *did* use the proxy | safehttp never touched it; `Proxy: nil` is genuine |
| Plaintext downgrade, mixed public+private A records, DNS rebinding, `::ffff:127.0.0.1`, NAT64, 6to4, Teredo, CGNAT, `0.0.0.0`, TEST-NETs | all refused |
| Homographs (Cyrillic/fullwidth/punycode), userinfo `user@ghcr.io`, `evilgcr.io`, `gcr.io.evil.com`, ports, `[::1]`, `0177.0.0.1`, CR/LF/NUL smuggling | all refused |

Resolve-once-then-dial-the-literal genuinely closes the rebinding window.
Credential audit confirms Q3: `authn.Anonymous` is the only `authn.` reference in
the tree. No error, log, or response leaks an upstream IP or distinguishes a
private repo from a missing one. The docker-save tar parser held: hostile member
names are map keys only and never reach a filesystem path.

## HIGH — must fix

- **H1 [repro] — unbounded layer entry count is a remote OOM.**
  `analyze/indexer.go:144`'s `entries` map is unbounded. `MaxLayers`, manifest
  size, and config size are capped; entry *count* is not. Measured **7.0 MB of
  gzip on the wire → 494 MB of heap, a 74:1 amplification**; ~55 MB of upload
  kills a 4 GB server, multiplied by 512 layers and unbounded concurrency.
  Reachable by anyone who can push a public image to GHCR, ACR, or Artifact
  Registry. It also quietly breaks §4.6's memory ceiling for a legitimately
  pathological image. **Fix:** cap entries per layer and classify it like
  `ErrTooManyLayers`.

- **H2 [repro] — no admission control on pulls.** `ingest/manager.go:287`: 400
  concurrent POSTs produced 400 goroutines and 400 real outbound sessions to
  ghcr.io, with the pull table reaching 400 despite `maxRetainedPulls = 64`
  (`evictLocked` only drops *terminal* pulls). This is the multiplier on every
  other finding, and it burns the shared Docker Hub anonymous quota.
  **Fix:** bounded worker pool / max in-flight, returning 429.

## MEDIUM

- **M1 [repro] — `--cache-max-bytes` does not bound peak disk.**
  `cachestore/store.go:561,576` write the staged index in full *before*
  `reserve` at :584. With a 1 MiB cap, peak on-disk hit **12,104,078 bytes
  (11.5×)** before `ErrCacheFull`. Accounting stays correct — the earlier
  concurrent-`PutLayer` fix holds under `-race` and via the pull path.
  **Fix:** charge the staged write against remaining budget as it streams.
- **M2 [repro] — no deadline on a pull or its body.** `safehttp.go:77` bounds
  only time-to-first-byte; `Client()` sets no `Timeout`; `manager.go:292` uses
  `context.WithCancel(context.Background())`. A trickling upstream held a
  connection 29.8 s for 1000 bytes, indefinitely extendable.
  **Fix:** a stall detector (minimum throughput) or an overall pull deadline.

## LOW

- **L1 [repro] — `..`/`.` accepted in a registry repository path.**
  `imgref.Parse` has no traversal check, unlike `validLocalReference`
  (`manager.go:212`) added for the Docker path in this same commit.
  `ghcr.io/../../secret` produces an un-normalized
  `GET /v2/../../secret/manifests/v1`, and ghcr.io's token endpoint saw
  `scope=repository%3A..%2F..%2Fsecret%3Apull`. The host cannot change, so impact
  is limited to an arbitrary anonymous GET path on an already-allowlisted
  registry — but the fix already exists ten lines away.
- **L2 [repro] — a double trailing dot slips normalization.** `imgref.go:160`
  trims one dot and `Allows` at :95 trims another, so `ghcr.io../o/i` is accepted
  and carried as registry `ghcr.io.`, giving a second idempotency key and a
  non-canonical `ServerName`. Three dots correctly fail.

## Informational

`DOCKER_HOST` is accepted verbatim including `tcp://` — the daemon path bypasses
safehttp by design, but nothing enforces a unix socket. `reservedV6`'s comment
names `5f00::/16` but the slice omits it. `MaxResponseHeaderBytes` is unset
(10 MiB stdlib default × concurrency). Pull IDs are `p<unixnano>-<seq>` and any
client can cancel any pull — moot while `GET /api/v1/pulls` is unauthenticated
and lists them all.

## If this were ever exposed publicly

Required, in order: an entry-count cap (H1), a concurrent-pull limit (H2), a
pull deadline (M2), and an auth gate. Unauthenticated and unthrottled, an
anonymous client gets a remote OOM in one request, a disk-fill past the
configured cap, and an outbound-request amplifier aimed at five major registries
from the server's own IP.
