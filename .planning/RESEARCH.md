# Research & Clarifications

Answers from the user to open questions raised during planning. This file is the
authority when it conflicts with an earlier reading of `PROJECT.md`.

## Round 1 — asked before technical research completed (2026-08-29)

### Q1. How should `mise run deploy` to exe.dev be handled?

**Answer: Build it, don't run it.**

Write the systemd unit and the `mise run deploy` task in full, parameterized by
environment variables (e.g. `LAYERLENS_DEPLOY_HOST`, `LAYERLENS_DEPLOY_USER`,
`LAYERLENS_DEPLOY_DIR`), with a documented dry-run mode. No SSH is attempted from
the dev sandbox.

**Implications**
- Deploy is a real deliverable, not a stub: unit file, deploy task, cross-compile
  to `linux/amd64`, restart via `systemctl`.
- It must be verifiable without a target host — the dry-run path and a shellcheck /
  syntax pass are the acceptance test.

### Q2. How should the demo `example:v1` / `example:v2` images be produced?

**Answer: Vendor prebuilt OCI tarballs.**

Commit OCI-layout image fixtures to the repo rather than building via a local
Docker daemon at first run.

**Implications**
- Zero dependency on Docker or the network for the demo *and* for e2e tests —
  the golden workflow is fully deterministic and reproducible on any machine.
- The fixtures must still be *shaped* like the real Dockerfile in `PROJECT.md`
  (node base → `WORKDIR` → `COPY . .` → `npm install` → `apt-get install ffmpeg`)
  so the demo actually demonstrates the cache-invalidation lesson: identical base
  layers, then a diverging `COPY` layer (v2 carries extra files that a
  `.dockerignore` should have excluded), which then invalidates the otherwise
  identical `npm install` layer downstream.
- Fixtures are produced by a committed, deterministic **generator** (checked-in Go
  or script) so they can be regenerated and reviewed, not opaque blobs.
- Ingesting from a live Docker socket is still a required feature (it is one of the
  three image sources in `PROJECT.md`); it is just not how the demo images arrive.
- `PROJECT.md`'s "start off by downloading the pre-specified images" becomes
  "load the pre-specified image fixtures from disk at startup" for the example
  images. Any additional non-example seed images may still be pulled from a
  registry, but startup must succeed with no network.

### Q3. What registry authentication should the backend support?

**Answer: Anonymous public pulls only.**

**Implications**
- No credential storage, no `~/.docker/config.json` reading, no credential helpers,
  no cloud SDK auth chains. Anonymous bearer-token flow only.
- Substantially reduces attack surface and makes the SSRF allowlist the single
  security control that matters for user-supplied references.
- Private repositories are explicitly unsupported; the API must return a clear,
  non-leaky error rather than prompting for credentials.

### Q4. Which of the 5 registries get real coverage?

**Answer: Deep on 2, allowlist all 5.**

**Implications**
- Docker Hub and GHCR are exercised end-to-end with integration tests against real
  anonymous-pullable images.
- GCR, ACR, and ECR go through the identical code path and are on the allowlist,
  but are not verified against the live services. This is documented as a known
  limitation rather than an implied guarantee.
- Integration tests that hit the network must be behind a build tag / opt-in flag
  so the default `mise run test` stays hermetic.

## Round 2 — asked after technical research (2026-08-29)

Raised by `DECISIONS.md` "Open questions for the user". Two of its five questions
(private-registry credentials; how demo images are produced) were already settled in
Round 1 above as *anonymous-only* and *vendored OCI fixtures*.

### Q5. Does "could-be-shared" require matching file metadata, or just content?

**Answer: content + permission bits; ignore mtime and uid/gid.**

Two layers are candidates for sharing when their normalized changesets match on
`(path, kind, content sha256, mode bits, link target)`. Modification times and
uid/gid are excluded from the comparison.

**Rationale / implications**
- mtime *must* be excluded: it is exactly what makes DiffIDs differ between two
  builds that produced byte-identical content, which is the whole phenomenon the
  tool exists to reveal.
- Mode bits *must* be included: claiming two layers are interchangeable when one
  ships a non-executable binary would be a wrong answer, not a permissive one.
- uid/gid excluded as a deliberate trade-off — build-context ownership varies with
  the builder's environment while the resulting image behaves identically.
- This is the **normalized changeset digest**; it is distinct from the DiffID, and
  both are computed and stored. DiffID drives the shared-trunk (ChainID) logic;
  the changeset digest drives the dotted "could-be-shared" edges. Never conflate
  the two — the UI must not present a changeset match as an actual cache hit.

### Q6. Is the deployed exe.dev instance publicly reachable?

**Answer: private / trusted network only.**

**Implications**
- No authentication layer, no per-user quotas, no rate limiting required.
- The registry allowlist remains the single security control that matters, and it
  is there to prevent SSRF from the *server's* network position, not to police
  users.
- The systemd unit should still apply ordinary hardening (dedicated user,
  `ProtectSystem`, `NoNewPrivileges`, private tmp) — cheap and correct regardless
  of exposure.
- Documented assumption: if this is ever exposed publicly, the 25 GiB pull endpoint
  becomes a bandwidth/disk amplifier and needs auth + caps first. Note this in the
  README rather than building it now.

### Q7. Disk budget and eviction for the on-server cache?

**Answer: configurable cap with LRU eviction.**

- A `--cache-max-bytes` flag (default ~50 GiB) bounds the cache.
- Least-recently-used eviction reclaims space; "recently used" is tracked per
  cached image, and eviction is atomic enough that a concurrent read never sees a
  half-deleted entry.
- A single image that would not fit under the cap is **refused with a clear error**
  rather than triggering a thrash-evict of everything else.
- This is testable without large images by setting a tiny cap in tests — make the
  cap injectable, not a hardcoded constant.

### Q8. TypeScript 5.9.3 or 7.0.2 (the native compiler rewrite)?

**Answer: 5.9.3.**

Known-compatible with typescript-eslint 8.68 and the rest of the chosen toolchain.
The project's risk budget belongs in the Docker/OCI analysis, not in the build
pipeline. Revisit only if typecheck time becomes a real problem.
