# Phase 009 — Deployment & release hardening

## Goal

Deliver deployment as a real, verifiable artifact without ever contacting a
server (RESEARCH Q1: "build it, don't run it"): a hardened systemd unit, a
cross-compilation task for linux/amd64, and a `mise run deploy` task that
transfers the binary, fixtures, and unit over SSH and restarts the service —
fully parameterized by environment variables and provably correct via a
documented dry-run mode. Close out the release: README, final UAT sweep, and
plan-status bookkeeping.

## Scope

**In:** `deploy/layerlens.service`; `deploy/deploy.sh`; mise tasks
`build-linux` and `deploy`; dry-run mode; shellcheck + unit verification;
README (run/deploy instructions, RESEARCH Q6 public-exposure caveat, RESEARCH
Q4 + ARCHITECTURE §10.9 known limitations); a full pass of the ARCHITECTURE
§9.5 UAT checklist on the final build; final status updates in
IMPLEMENTATION.md.

**Not in this phase:** actually SSHing anywhere or enabling the service on a
real host (explicitly excluded by RESEARCH Q1); CI; auth/rate limiting
(RESEARCH Q6 defers to README note).

## Prerequisites

Phases 001–008 complete (deploys the finished binary + fixtures).

## Files to create/modify

- `deploy/layerlens.service` — per ARCHITECTURE §1.3: dedicated `layerlens`
  user (`DynamicUser=` or documented `useradd`), `StateDirectory=layerlens`,
  `ProtectSystem=strict`, `NoNewPrivileges=yes`, `PrivateTmp=yes`,
  `Restart=on-failure`, `ExecStart=/opt/layerlens/layerlens --listen :8080
  --data-dir /var/lib/layerlens/images --fixtures-dir /opt/layerlens/fixtures`,
  plus an `ExecStartPost` healthz curl check.
- `deploy/deploy.sh` — POSIX-safe bash, `set -euo pipefail`; reads
  `LAYERLENS_DEPLOY_HOST`, `LAYERLENS_DEPLOY_USER`, `LAYERLENS_DEPLOY_DIR`
  (documented defaults/required-ness; fail fast with usage when missing);
  steps: scp binary + `fixtures/` + unit → remote tmp, atomic move,
  `systemctl daemon-reload`, `systemctl restart layerlens`,
  `systemctl is-active` check; `LAYERLENS_DEPLOY_DRY_RUN=1` (or `--dry-run`)
  prints every command it *would* run, verbatim, executes nothing remote.
- `mise.toml` — `[tasks.build-linux] run = "CGO_ENABLED=0 GOOS=linux
  GOARCH=amd64 go build -o bin/layerlens-linux-amd64 ./cmd/layerlens"`
  (depends on `build-web`); `[tasks.deploy] depends = ["build-linux"]
  run = "./deploy/deploy.sh"`.
- `README.md` — quickstart (`mise install`, `mise run build`, run, demo
  walkthrough), deploy docs (env vars, dry run), security posture (private
  network assumption + "if exposed publicly, the 25 GiB pull endpoint needs
  auth and caps first" per RESEARCH Q6), known limitations (GCR/ACR/ECR not
  live-verified per RESEARCH Q4; zstd layers untested live per §10.9;
  in-memory pull state per §10.4).
- `.planning/IMPLEMENTATION.md` — final statuses.

## Implementation steps

1. Write the unit; validate: `systemd-analyze verify` where a systemd host is
   available (the Linux dev sandbox), otherwise a strict review checklist
   against §1.3 + RESEARCH Q6 hardening list committed alongside.
2. Write `deploy.sh` with the dry-run short-circuit wrapping every remote
   command through one `run()` helper (echoes in dry-run, executes otherwise)
   so dry-run output is *exactly* the real command list.
3. `shellcheck deploy/deploy.sh` clean; add it to `mise run lint`.
4. Wire mise tasks; confirm `mise run build-linux` cross-compiles from the
   dev host (pure-Go, CGO off — DECISIONS risk 3) and `file` reports an
   x86-64 Linux static binary.
5. `mise run deploy` with dry-run env set → prints the full command plan,
   touches no network (verify: run with `LAYERLENS_DEPLOY_HOST=example.invalid`).
6. README; final UAT sweep of ARCHITECTURE §9.5 items 1–14 (items 11–12 need
   the opt-in docker/network environments; record results); fix or file
   deltas per the workflow rule.
7. Final gates across the whole repo; update statuses; commit.

## Test cases

- `deploy_dry_run_prints_full_plan` (bats-style or a Go/exec test invoking
  the script with `LAYERLENS_DEPLOY_DRY_RUN=1` and fake env): output contains
  scp of binary, fixtures, and unit, `daemon-reload`, `restart`, `is-active`,
  in order; exit 0; no processes spawned other than echoes (assert no `ssh`
  in PATH is needed by stubbing PATH).
- `deploy_missing_env_fails_with_usage`: unset host → non-zero exit + usage
  text naming all three variables.
- `shellcheck_clean` (lint task).
- `cross_compile_artifact`: build task produces `bin/layerlens-linux-amd64`;
  test asserts GOOS/GOARCH via `go version -m` output.
- Unit-file review checklist committed with each §1.3/Q6 hardening directive
  checked off (mechanical verification where tooling exists).

## Acceptance criteria

- `mise run deploy` in dry-run mode is a complete, human-auditable rehearsal:
  every remote command printed verbatim, zero network attempted (RESEARCH Q1's
  acceptance test, verbatim).
- The systemd unit contains, at minimum: dedicated user, `ProtectSystem=strict`,
  `NoNewPrivileges=yes`, `PrivateTmp=yes`, `StateDirectory=layerlens`,
  `Restart=on-failure` (RESEARCH Q6 hardening list).
- `bin/layerlens-linux-amd64` is a statically linked linux/amd64 binary built
  with `CGO_ENABLED=0`, with the SPA and current fixtures deployable beside it.
- README documents run, demo, deploy env vars, dry run, the private-network
  assumption, and the known-limitation list.
- Full-repo `mise run lint && mise run typecheck && mise run test && mise run
  e2e` green; §9.5 UAT checklist executed and recorded.
- Every phase row in IMPLEMENTATION.md reads `Complete`.

## Risks / gotchas

- **Never let a "quick test" of deploy.sh reach a real host** — default to
  dry-run when `LAYERLENS_DEPLOY_HOST` is unset is *not* enough; the script
  must hard-require the env var so an accidental bare run fails, and the
  sandbox has no SSH target anyway (RESEARCH Q1).
- `ProtectSystem=strict` + `StateDirectory` means the binary must write ONLY
  under `/var/lib/layerlens` — if any code writes elsewhere (temp files
  outside `PrivateTmp`, etc.) the service breaks at runtime on a host we
  can't test; audit write paths against the unit.
- `ExecStartPost` healthz check needs curl on the host — guard with `-` prefix
  or document the dependency.
- Fixtures must ship with the deploy (startup requires them for pinned demo
  images, §1.2) — forgetting them makes healthz never open.
- Cross-compile from arm64 dev to amd64 is pure-Go safe (DECISIONS risk 3),
  but verify no dependency sneaks in cgo (`go build` with `CGO_ENABLED=0`
  fails loudly if one does — keep it that way).
