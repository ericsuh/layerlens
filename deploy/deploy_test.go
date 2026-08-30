// Package deploy_test verifies the deployment artifacts — the systemd unit and
// deploy.sh — without ever contacting a host (RESEARCH Q1: the deploy is built
// and rehearsed, never run). The dry-run plan and the unit's hardening
// directives are the two things that can be checked mechanically, so they are.
package deploy_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPATH builds a PATH whose ssh and scp abort loudly. A dry run that
// executed either would print the marker below, which every assertion here
// treats as a failure: the promise is that the rehearsal opens no socket, and
// the only way to prove it is to make a real invocation impossible.
const sabotageMarker = "DEPLOY_TEST_COMMAND_WAS_EXECUTED"

func stubPATH(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, name := range []string{"ssh", "scp"} {
		script := "#!/bin/sh\necho " + sabotageMarker + " \"$0 $*\" >&2\nexit 99\n"
		require.NoError(t, os.WriteFile(filepath.Join(dir, name), []byte(script), 0o755))
	}
	return dir + string(os.PathListSeparator) + os.Getenv("PATH")
}

// runDeploy invokes deploy.sh from the repository root with a controlled
// environment, and returns its combined output and exit code.
func runDeploy(t *testing.T, env []string, args ...string) (string, int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("deploy.sh is a POSIX shell script")
	}
	cmd := exec.Command("bash", append([]string{"deploy/deploy.sh"}, args...)...)
	// From the repository root: deploy.sh's default paths (bin/, fixtures/,
	// deploy/) are repo-relative, exactly as `mise run deploy` invokes it.
	cmd.Dir = ".."
	// PATH, HOME and the stub directory only: deploy.sh must not depend on
	// anything inherited from a developer's shell.
	cmd.Env = append([]string{"PATH=" + stubPATH(t), "HOME=" + t.TempDir()}, env...)
	out, err := cmd.CombinedOutput()
	code := 0
	var exitErr *exec.ExitError
	if err != nil {
		require.ErrorAs(t, err, &exitErr, "deploy.sh failed to execute at all")
		code = exitErr.ExitCode()
	}
	return string(out), code
}

func TestDeployDryRunPrintsFullPlan(t *testing.T) {
	out, code := runDeploy(t, []string{
		"LAYERLENS_DEPLOY_HOST=example.invalid",
		"LAYERLENS_DEPLOY_DRY_RUN=1",
	})
	require.Equal(t, 0, code, "dry run must succeed:\n%s", out)
	require.NotContains(t, out, sabotageMarker,
		"the dry run executed a remote command; it must only print them:\n%s", out)

	// Every remote step, in the order the deploy performs them. Ordering is the
	// safety property under test: the binary is staged and swapped before the
	// unit is installed, and the service is restarted before it is verified.
	wantInOrder := []string{
		"useradd --system",
		"scp -P 22 -o BatchMode=yes -o ConnectTimeout=10 bin/layerlens-linux-amd64 root@example.invalid:/opt/layerlens/.layerlens.",
		"scp -P 22 -o BatchMode=yes -o ConnectTimeout=10 -r fixtures root@example.invalid:/opt/layerlens/.fixtures.",
		"deploy/layerlens.service root@example.invalid:/opt/layerlens/.layerlens.",
		"mv -f /opt/layerlens/.layerlens.",
		"/opt/layerlens/layerlens",
		"mv /opt/layerlens/.fixtures.",
		"install -o root -g root -m 0644",
		"/etc/systemd/system/layerlens.service",
		"systemctl daemon-reload",
		"systemctl enable layerlens",
		"systemctl restart layerlens",
		"systemctl --no-pager --quiet is-active layerlens",
		"curl --fail --silent --max-time 5 http://127.0.0.1:8080/healthz",
		"journalctl --unit layerlens",
	}
	rest := out
	for _, want := range wantInOrder {
		idx := strings.Index(rest, want)
		require.GreaterOrEqualf(t, idx, 0,
			"dry-run plan is missing %q (or it appears out of order)\nfull output:\n%s", want, out)
		rest = rest[idx+len(want):]
	}
	assert.Contains(t, out, "DRY RUN")
	assert.Contains(t, out, "Dry run complete.")
	// The step counter must not run past its own announced total.
	steps := regexp.MustCompile(`\[(\d+)/(\d+)\]`).FindAllStringSubmatch(out, -1)
	require.NotEmpty(t, steps, "the plan must be numbered")
	for _, m := range steps {
		n, err := strconv.Atoi(m[1])
		require.NoError(t, err)
		total, err := strconv.Atoi(m[2])
		require.NoError(t, err)
		assert.LessOrEqualf(t, n, total, "step %d exceeds the announced total %d", n, total)
	}
}

func TestDeployDryRunHonorsEnvironmentOverrides(t *testing.T) {
	out, code := runDeploy(t, []string{
		"LAYERLENS_DEPLOY_HOST=box.internal",
		"LAYERLENS_DEPLOY_USER=deployer",
		"LAYERLENS_DEPLOY_DIR=/srv/layerlens",
		"LAYERLENS_DEPLOY_PORT=2222",
		"LAYERLENS_DEPLOY_SERVICE=layerlens-staging",
		"LAYERLENS_DEPLOY_HEALTH_URL=http://127.0.0.1:9090/healthz",
		"LAYERLENS_DEPLOY_DRY_RUN=1",
	})
	require.Equal(t, 0, code, "dry run must succeed:\n%s", out)
	require.NotContains(t, out, sabotageMarker)

	assert.Contains(t, out, "ssh -p 2222")
	assert.Contains(t, out, "deployer@box.internal")
	assert.Contains(t, out, "/srv/layerlens/layerlens")
	assert.Contains(t, out, "/etc/systemd/system/layerlens-staging.service")
	assert.Contains(t, out, "http://127.0.0.1:9090/healthz")
	// A non-root SSH user must acquire privilege for the root-only steps, and
	// non-interactively so a password prompt fails instead of hanging.
	assert.Contains(t, out, "sudo -n systemctl restart layerlens-staging")
	assert.NotContains(t, out, "root@box.internal")
}

// The --dry-run flag and LAYERLENS_DEPLOY_DRY_RUN=1 must be interchangeable.
func TestDeployDryRunFlagMatchesEnvVar(t *testing.T) {
	env := []string{"LAYERLENS_DEPLOY_HOST=example.invalid"}
	viaFlag, flagCode := runDeploy(t, env, "--dry-run")
	viaEnv, envCode := runDeploy(t, append(env, "LAYERLENS_DEPLOY_DRY_RUN=1"))
	require.Equal(t, 0, flagCode, viaFlag)
	require.Equal(t, 0, envCode, viaEnv)

	// The staging paths carry a timestamp and PID, so compare with those
	// normalized away; everything else must match exactly.
	norm := regexp.MustCompile(`\.(layerlens|fixtures)\.[0-9TZ]+-\d+`)
	assert.Equal(t,
		norm.ReplaceAllString(viaFlag, ".STAMP"),
		norm.ReplaceAllString(viaEnv, ".STAMP"))
}

func TestDeployMissingEnvFailsWithUsage(t *testing.T) {
	out, code := runDeploy(t, nil)
	assert.Equal(t, 2, code, "a deploy with no target must fail, not guess:\n%s", out)
	assert.NotContains(t, out, sabotageMarker)
	// The usage text must name every variable an operator has to know about.
	for _, v := range []string{
		"LAYERLENS_DEPLOY_HOST",
		"LAYERLENS_DEPLOY_USER",
		"LAYERLENS_DEPLOY_DIR",
		"LAYERLENS_DEPLOY_DRY_RUN",
	} {
		assert.Contains(t, out, v)
	}
	// Critically: no fallback to dry-run, which would make a bare run look
	// like it succeeded.
	assert.NotContains(t, out, "Dry run complete.")
}

func TestDeployRejectsUnknownArguments(t *testing.T) {
	out, code := runDeploy(t, []string{"LAYERLENS_DEPLOY_HOST=example.invalid"}, "--yolo")
	assert.Equal(t, 2, code)
	assert.Contains(t, out, "unknown argument --yolo")
	assert.NotContains(t, out, sabotageMarker)
}

// A binary for the wrong platform must be caught locally, before any bytes are
// pushed to a server that would fail to exec it.
func TestDeployRejectsWrongArchitectureBinary(t *testing.T) {
	bogus := filepath.Join(t.TempDir(), "layerlens-linux-amd64")
	require.NoError(t, os.WriteFile(bogus, []byte("#!/bin/sh\necho not an elf\n"), 0o755))

	out, code := runDeploy(t, []string{
		"LAYERLENS_DEPLOY_HOST=example.invalid",
		"LAYERLENS_DEPLOY_BINARY=" + bogus,
	})
	assert.Equal(t, 1, code, "a non-amd64 binary must abort the deploy:\n%s", out)
	assert.Contains(t, out, "expected linux-amd64")
	assert.NotContains(t, out, sabotageMarker)
}

// TestCrossCompiledArtifact checks the artifact `mise run build-linux` produces.
// It is skipped rather than failed when the artifact is absent, so `go test
// ./...` on a fresh clone does not require a 30-second cross-compile.
func TestCrossCompiledArtifact(t *testing.T) {
	const path = "../bin/layerlens-linux-amd64"
	if _, err := os.Stat(path); err != nil {
		t.Skip("bin/layerlens-linux-amd64 not built; run: mise run build-linux")
	}

	out, err := exec.Command("go", "version", "-m", path).CombinedOutput()
	require.NoError(t, err, string(out))
	for _, want := range []string{"GOOS=linux", "GOARCH=amd64", "CGO_ENABLED=0"} {
		assert.Contains(t, string(out), want,
			"the deploy artifact must be a static linux/amd64 cross-compile")
	}

	// ELF header: 0x7f E L F, 64-bit class, e_machine = EM_X86_64 (0x3e).
	f, err := os.Open(path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, f.Close()) }()
	hdr := make([]byte, 20)
	_, err = f.Read(hdr)
	require.NoError(t, err)
	assert.Equal(t, []byte("\x7fELF"), hdr[:4])
	assert.EqualValues(t, 2, hdr[4], "ELF class must be 64-bit")
	assert.EqualValues(t, 0x3e, hdr[18], "e_machine must be EM_X86_64")
	assert.EqualValues(t, 0x00, hdr[19])
}

// TestUnitFileHardening mechanizes the review checklist the phase plan asked
// for: every directive ARCHITECTURE §1.3 and RESEARCH Q6 call for is asserted
// present with its intended value, so the unit cannot silently lose one.
func TestUnitFileHardening(t *testing.T) {
	raw, err := os.ReadFile("layerlens.service")
	require.NoError(t, err)

	directives := map[string][]string{}
	var section string
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		directives[section+"."+key] = append(directives[section+"."+key], value)
	}

	// Exact-value requirements.
	for key, want := range map[string]string{
		// ARCHITECTURE §1.3 + RESEARCH Q6 minimum hardening set.
		"Service.User":            "layerlens",
		"Service.StateDirectory":  "layerlens",
		"Service.ProtectSystem":   "strict",
		"Service.ProtectHome":     "yes",
		"Service.NoNewPrivileges": "yes",
		"Service.PrivateTmp":      "yes",
		"Service.Restart":         "on-failure",
		// Capability bounding: an empty set, not merely a reduced one.
		"Service.CapabilityBoundingSet": "",
		"Service.AmbientCapabilities":   "",
		// Journald logging.
		"Service.StandardOutput":   "journal",
		"Service.StandardError":    "journal",
		"Service.SyslogIdentifier": "layerlens",
		// A failed exec must be reported as a failed start.
		"Service.Type": "exec",
	} {
		values := directives[key]
		require.Lenf(t, values, 1, "%s must be set exactly once, got %v", key, values)
		assert.Equalf(t, want, values[0], "%s", key)
	}

	// Present-with-any-value requirements.
	for _, key := range []string{
		"Service.RestrictAddressFamilies",
		"Service.RestrictNamespaces",
		"Service.RestrictRealtime",
		"Service.RestrictSUIDSGID",
		"Service.SystemCallFilter",
		"Service.SystemCallArchitectures",
		"Service.LockPersonality",
		"Service.MemoryDenyWriteExecute",
		"Service.ProtectKernelTunables",
		"Service.ProtectKernelModules",
		"Service.ProtectKernelLogs",
		"Service.ProtectControlGroups",
		"Service.ProtectClock",
		"Service.ProtectHostname",
		"Service.PrivateDevices",
		// Resource limits (phase 009 scope).
		"Service.MemoryMax",
		"Service.TasksMax",
		"Service.LimitNOFILE",
		// Graceful restart must outlast the server's own drain budget.
		"Service.TimeoutStopSec",
		"Service.KillSignal",
		// Health gate.
		"Service.ExecStartPost",
		"Install.WantedBy",
	} {
		assert.Containsf(t, directives, key, "%s is missing from the unit", key)
	}

	// The writable state directory and the flags must agree: --data-dir has to
	// live under /var/lib/layerlens or ProtectSystem=strict makes the server
	// unable to write its cache at all.
	require.Len(t, directives["Service.ExecStart"], 1)
	execStart := directives["Service.ExecStart"][0]
	assert.Contains(t, execStart, "/opt/layerlens/layerlens")
	assert.Contains(t, execStart, "--data-dir ${LAYERLENS_DATA_DIR}")
	dataDir := ""
	for _, env := range directives["Service.Environment"] {
		if v, ok := strings.CutPrefix(env, "LAYERLENS_DATA_DIR="); ok {
			dataDir = v
		}
	}
	assert.True(t, strings.HasPrefix(dataDir, "/var/lib/layerlens"),
		"the data directory %q must be inside StateDirectory=layerlens", dataDir)

	// The Docker socket needs a root-equivalent group; it must stay opt-in.
	assert.NotContains(t, directives, "Service.SupplementaryGroups",
		"SupplementaryGroups=docker must stay commented out — it is root-equivalent")
	for _, env := range directives["Service.Environment"] {
		if v, ok := strings.CutPrefix(env, "LAYERLENS_DOCKER_HOST="); ok {
			assert.Equal(t, "off", v, "the daemon source must default to off in the unit")
		}
	}
}
