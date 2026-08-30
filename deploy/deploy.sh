#!/usr/bin/env bash
#
# deploy.sh — ship layerlens to a linux/amd64 host over SSH and restart it.
#
# Everything is parameterized by environment variables (RESEARCH Q1); only
# LAYERLENS_DEPLOY_HOST is required, and there is no default for it on purpose,
# so a bare `./deploy/deploy.sh` cannot accidentally reach a host.
#
#   LAYERLENS_DEPLOY_DRY_RUN=1 ./deploy/deploy.sh
#
# prints the exact command plan — every remote command verbatim and shell-quoted
# — and executes nothing and opens no socket. That rehearsal is the acceptance
# test for the deploy path (RESEARCH Q1: build it, don't run it).
#
# Ordering matters and is the safety property: the new binary is uploaded to a
# staging path *inside the deploy directory* and moved into place with a single
# rename(2). Same filesystem, so the swap is atomic — a half-transferred binary
# is never at the path systemd execs, and renaming over a running executable
# replaces the directory entry while the live process keeps its old inode (a
# plain overwrite would instead fail with ETXTBSY).

set -euo pipefail

usage() {
	cat <<'EOF'
Usage: deploy/deploy.sh [--dry-run] [--help]

Deploys the cross-compiled binary, the demo fixtures and the systemd unit to a
Linux server, then reloads and restarts the service and waits for /healthz.

Required:
  LAYERLENS_DEPLOY_HOST       Target hostname or IP. No default: a deploy must
                              always name its target explicitly.

Optional (shown with their defaults):
  LAYERLENS_DEPLOY_USER=root          SSH user. Needs sudo unless it is root.
  LAYERLENS_DEPLOY_DIR=/opt/layerlens Remote install directory (binary+fixtures).
  LAYERLENS_DEPLOY_PORT=22            SSH port.
  LAYERLENS_DEPLOY_SERVICE=layerlens  systemd unit name.
  LAYERLENS_DEPLOY_BINARY=bin/layerlens-linux-amd64
                                      Local binary to ship (mise run build-linux).
  LAYERLENS_DEPLOY_FIXTURES=fixtures  Local fixtures directory to ship.
  LAYERLENS_DEPLOY_UNIT=deploy/layerlens.service
                                      Local unit file to install.
  LAYERLENS_DEPLOY_HEALTH_URL=http://127.0.0.1:8080/healthz
                                      Probed ON THE REMOTE HOST after restart.
  LAYERLENS_DEPLOY_HEALTH_RETRIES=30  Health poll attempts, 2s apart.
  LAYERLENS_DEPLOY_SUDO               Privilege prefix for root-only commands.
                                      Default: empty for root, "sudo -n" otherwise.
  LAYERLENS_DEPLOY_SSH_OPTS           Extra ssh/scp options, split on whitespace.
  LAYERLENS_DEPLOY_DRY_RUN=1          Print the plan; run nothing, dial nothing.

Examples:
  LAYERLENS_DEPLOY_HOST=layerlens.example.internal mise run deploy
  LAYERLENS_DEPLOY_HOST=layerlens.example.internal mise run deploy-dry-run
EOF
}

# --- configuration ----------------------------------------------------------

DRY_RUN="${LAYERLENS_DEPLOY_DRY_RUN:-0}"
for arg in "$@"; do
	case "$arg" in
	--dry-run) DRY_RUN=1 ;;
	-h | --help)
		usage
		exit 0
		;;
	*)
		printf 'deploy: unknown argument %s\n\n' "$arg" >&2
		usage >&2
		exit 2
		;;
	esac
done

HOST="${LAYERLENS_DEPLOY_HOST:-}"
USER_NAME="${LAYERLENS_DEPLOY_USER:-root}"
DIR="${LAYERLENS_DEPLOY_DIR:-/opt/layerlens}"
PORT="${LAYERLENS_DEPLOY_PORT:-22}"
SERVICE="${LAYERLENS_DEPLOY_SERVICE:-layerlens}"
BINARY="${LAYERLENS_DEPLOY_BINARY:-bin/layerlens-linux-amd64}"
FIXTURES="${LAYERLENS_DEPLOY_FIXTURES:-fixtures}"
UNIT="${LAYERLENS_DEPLOY_UNIT:-deploy/layerlens.service}"
HEALTH_URL="${LAYERLENS_DEPLOY_HEALTH_URL:-http://127.0.0.1:8080/healthz}"
HEALTH_RETRIES="${LAYERLENS_DEPLOY_HEALTH_RETRIES:-30}"
SERVICE_USER="${LAYERLENS_DEPLOY_SERVICE_USER:-layerlens}"

if [ -z "$HOST" ]; then
	cat >&2 <<'EOF'
deploy: LAYERLENS_DEPLOY_HOST is not set.

A deploy must name its target explicitly — there is deliberately no default
host, and no implicit fallback to dry-run, so that a bare run fails instead of
guessing. Set at least LAYERLENS_DEPLOY_HOST; LAYERLENS_DEPLOY_USER (default
root) and LAYERLENS_DEPLOY_DIR (default /opt/layerlens) are optional.

EOF
	usage >&2
	exit 2
fi

# Privilege prefix: nothing when connecting as root, `sudo -n` otherwise. -n so
# a host that would prompt for a password fails immediately instead of hanging
# on a tty that a non-interactive ssh does not have.
if [ -n "${LAYERLENS_DEPLOY_SUDO+set}" ]; then
	SUDO="$LAYERLENS_DEPLOY_SUDO"
elif [ "$USER_NAME" = "root" ]; then
	SUDO=""
else
	SUDO="sudo -n"
fi
# Interpolated form: the prefix plus its separating space, or nothing at all, so
# an empty SUDO does not leave stray whitespace in the printed plan.
SUDO_P=""
[ -n "$SUDO" ] && SUDO_P="$SUDO "

# Word-split on purpose: these are option lists, not single arguments.
# shellcheck disable=SC2206
EXTRA_OPTS=(${LAYERLENS_DEPLOY_SSH_OPTS:-})
SSH=(ssh -p "$PORT" -o BatchMode=yes -o ConnectTimeout=10 "${EXTRA_OPTS[@]}" "${USER_NAME}@${HOST}")
SCP=(scp -P "$PORT" -o BatchMode=yes -o ConnectTimeout=10 "${EXTRA_OPTS[@]}")

# One stamp for the whole run, so the staging paths of a single deploy are
# recognizable as a set in `ls` on the remote host and cannot collide with a
# concurrent deploy's.
STAMP="$(date -u +%Y%m%dT%H%M%SZ)-$$"
STAGE_BIN="${DIR}/.layerlens.${STAMP}.new"
STAGE_UNIT="${DIR}/.layerlens.${STAMP}.service"
STAGE_FIXTURES="${DIR}/.fixtures.${STAMP}"

# --- plumbing ---------------------------------------------------------------

# quote renders one argument the way a shell would need it written. The dry-run
# plan is meant to be read and pasted, so this quotes only what actually needs
# it and wraps everything else in single quotes (POSIX: the only character that
# cannot appear inside '' is ', handled by closing, escaping and reopening).
quote() {
	case "$1" in
	*[!A-Za-z0-9_@%+=:,./-]* | '') printf "'%s'" "${1//\'/\'\\\'\'}" ;;
	*) printf '%s' "$1" ;;
	esac
}

quote_all() {
	local first=1 arg
	for arg in "$@"; do
		if [ "$first" = 1 ]; then first=0; else printf ' '; fi
		quote "$arg"
	done
}

STEP=0
step() {
	STEP=$((STEP + 1))
	printf '\n\033[1m[%d/%d] %s\033[0m\n' "$STEP" "$TOTAL_STEPS" "$1"
}
TOTAL_STEPS=10

note() { printf '      %s\n' "$1"; }
warn() { printf 'deploy: WARNING: %s\n' "$1" >&2; }

# run executes one command, or prints it verbatim under --dry-run. Every command
# that touches the remote host goes through here and nowhere else, which is what
# makes the dry-run plan exactly the real command list rather than a narration
# of it.
run() {
	printf '      $ %s\n' "$(quote_all "$@")"
	if [ "$DRY_RUN" = 1 ]; then
		return 0
	fi
	"$@"
}

# remote runs a shell snippet on the target. The snippet is passed as a single
# argument so the local shell never expands it, and it is printed by run()
# exactly as the remote sh will receive it.
remote() {
	run "${SSH[@]}" "$@"
}

# --- local preflight --------------------------------------------------------
#
# These checks read local files only. They run in dry-run too — a plan that
# would fail on its first scp is not a useful rehearsal — but under dry-run a
# missing artifact is a warning rather than a fatal error, so the full plan
# still prints on a tree that has not been built yet.

preflight_fail() {
	if [ "$DRY_RUN" = 1 ]; then
		warn "$1"
		return 0
	fi
	printf 'deploy: %s\n' "$1" >&2
	exit 1
}

# elf_arch reports the ELF class/machine of a local file as a short string, so a
# darwin or arm64 binary is caught here rather than by systemd on the server.
# Bytes, not file(1): file(1) is not installed everywhere, od is.
elf_arch() {
	local hdr
	hdr="$(od -An -v -tx1 -N20 "$1" 2>/dev/null | tr -d ' \n')"
	case "$hdr" in
	7f454c46*) ;;
	*)
		printf 'not-an-elf'
		return 0
		;;
	esac
	# Offset 4 is EI_CLASS (02 = 64-bit); offsets 18..19 are e_machine,
	# little-endian (3e 00 = x86-64, b7 00 = aarch64).
	local class="${hdr:8:2}" machine="${hdr:36:4}"
	case "${class}/${machine}" in
	02/3e00) printf 'linux-amd64' ;;
	02/b700) printf 'linux-arm64' ;;
	*) printf 'elf-class%s-machine%s' "$class" "$machine" ;;
	esac
}

step "Preflight (local, reads nothing remote)"
if [ ! -f "$BINARY" ]; then
	preflight_fail "binary $BINARY not found — run: mise run build-linux"
else
	arch="$(elf_arch "$BINARY")"
	if [ "$arch" != "linux-amd64" ]; then
		preflight_fail "binary $BINARY is $arch, expected linux-amd64 — run: mise run build-linux"
	else
		note "binary   $BINARY ($(elf_arch "$BINARY"), $(wc -c <"$BINARY" | tr -d ' ') bytes)"
	fi
fi
if [ ! -d "$FIXTURES" ]; then
	# Not fatal even outside dry-run: the server logs a warning and serves
	# without demo images. Shipping none silently would be the surprise.
	warn "fixtures directory $FIXTURES not found — the demo images will be missing on the server"
else
	note "fixtures $FIXTURES ($(find "$FIXTURES" -type f | wc -l | tr -d ' ') files)"
fi
if [ ! -f "$UNIT" ]; then
	preflight_fail "unit file $UNIT not found"
else
	note "unit     $UNIT -> /etc/systemd/system/${SERVICE}.service"
fi
note "target   ${USER_NAME}@${HOST}:${PORT} ${DIR}"
note "sudo     ${SUDO:-(none, connecting as root)}"
if [ "$DRY_RUN" = 1 ]; then
	printf '\n\033[1mDRY RUN\033[0m — the commands below are printed, not executed. No network is touched.\n'
fi

# --- the plan ---------------------------------------------------------------

step "Create the install directory and the service user"
# Idempotent: both are no-ops on a host that has been deployed to before. The
# service user owns nothing under $DIR — it only reads there; its writable state
# is /var/lib/layerlens, created by systemd's StateDirectory=.
remote "set -eu
${SUDO_P}mkdir -p $(quote "$DIR")
${SUDO_P}chown $(quote "${USER_NAME}") $(quote "$DIR")
getent passwd $(quote "$SERVICE_USER") >/dev/null || ${SUDO_P}useradd --system --user-group --home-dir /var/lib/layerlens --shell /usr/sbin/nologin $(quote "$SERVICE_USER")"

step "Upload the binary to a staging path inside the install directory"
# Staged inside $DIR, not /tmp: rename(2) is only atomic within one filesystem,
# and /tmp is very often a different one (tmpfs, or its own partition).
run "${SCP[@]}" "$BINARY" "${USER_NAME}@${HOST}:${STAGE_BIN}"

step "Upload the fixtures and the unit file"
run "${SCP[@]}" -r "$FIXTURES" "${USER_NAME}@${HOST}:${STAGE_FIXTURES}"
run "${SCP[@]}" "$UNIT" "${USER_NAME}@${HOST}:${STAGE_UNIT}"

step "Swap the new binary into place atomically"
# Ownership and mode are set on the staging path, so the file at the final path
# is complete, root-owned and executable the instant it appears — the service the
# root systemd execs is never writable by the (possibly non-root) deploy user. `mv -f` on the same filesystem is rename(2):
# no window in which $DIR/layerlens is partial, and the running process keeps
# executing its own (now unlinked) inode until it is restarted below.
remote "set -eu
${SUDO_P}chown root:root $(quote "$STAGE_BIN")
${SUDO_P}chmod 0755 $(quote "$STAGE_BIN")
${SUDO_P}mv -f $(quote "$STAGE_BIN") $(quote "${DIR}/layerlens")"

step "Swap the fixtures, keeping the previous copy"
# A directory cannot be replaced by one rename, so this is the two-step: the
# window where $DIR/fixtures is briefly absent is harmless because the running
# server read its fixtures at startup and does not reread them, and the restart
# below happens after the new copy is in place.
remote "set -eu
${SUDO_P}rm -rf $(quote "${DIR}/fixtures.previous")
if [ -e $(quote "${DIR}/fixtures") ]; then ${SUDO_P}mv $(quote "${DIR}/fixtures") $(quote "${DIR}/fixtures.previous"); fi
${SUDO_P}mv $(quote "$STAGE_FIXTURES") $(quote "${DIR}/fixtures")
${SUDO_P}chmod -R a+rX $(quote "${DIR}/fixtures")"

step "Install the unit file and reload systemd"
remote "set -eu
${SUDO_P}install -o root -g root -m 0644 $(quote "$STAGE_UNIT") $(quote "/etc/systemd/system/${SERVICE}.service")
${SUDO_P}rm -f $(quote "$STAGE_UNIT")
${SUDO_P}systemctl daemon-reload
${SUDO_P}systemctl enable $(quote "$SERVICE")"

step "Restart the service gracefully"
# `restart`, not stop+start: systemd sends one SIGTERM and the server drains
# in-flight requests before exiting (TimeoutStopSec=30s in the unit covers its
# 15s drain budget). ExecStartPost in the unit already waits for /healthz, so
# this returns only once the new process is serving.
remote "set -eu
${SUDO_P}systemctl restart $(quote "$SERVICE")
${SUDO_P}systemctl --no-pager --quiet is-active $(quote "$SERVICE")"

step "Verify: poll the health endpoint on the remote host"
# Independent of the unit's own ExecStartPost probe, and phrased as a loop so a
# cold cache re-analyzing fixtures (503 "loading") is waited out rather than
# reported as a failed deploy. curl runs on the server: the health endpoint is
# never assumed to be reachable from here.
remote "set -eu
for attempt in \$(seq 1 $(quote "$HEALTH_RETRIES")); do
  body=\$(curl --fail --silent --max-time 5 $(quote "$HEALTH_URL") || true)
  if [ \"\$body\" = ok ]; then echo \"healthz: ok (attempt \$attempt)\"; exit 0; fi
  sleep 2
done
echo \"healthz: ${SERVICE} did not become ready\" >&2
exit 1"

step "Report: recent service log"
remote "${SUDO_P}journalctl --unit $(quote "$SERVICE") --no-pager --lines 20"

if [ "$DRY_RUN" = 1 ]; then
	printf '\n\033[1mDry run complete.\033[0m %d steps planned; nothing was executed.\n' "$STEP"
else
	printf '\n\033[1mDeployed.\033[0m %s is running %s on %s.\n' "$SERVICE" "${DIR}/layerlens" "$HOST"
fi
