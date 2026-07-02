//go:build linux

package commands

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ─── buildCreateUserCmd ───────────────────────────────────────────────────────

func TestBuildCreateUserCmd_ContainsUsername(t *testing.T) {
	t.Parallel()
	cmd := buildCreateUserCmd("alice")
	assert.Contains(t, cmd, "alice", "command should reference the username")
}

func TestBuildCreateUserCmd_DebianStyleFirst(t *testing.T) {
	t.Parallel()
	cmd := buildCreateUserCmd("alice")
	// adduser (Debian) must appear before useradd (RHEL) in the fallback chain.
	debianIdx := strings.Index(cmd, "adduser --disabled-password")
	rhelIdx := strings.Index(cmd, "useradd -m")
	assert.Greater(t, debianIdx, -1, "Debian-style adduser should be present")
	assert.Greater(t, rhelIdx, -1, "useradd should be present as fallback")
	assert.Less(t, debianIdx, rhelIdx, "Debian-style adduser should come before useradd")
}

func TestBuildCreateUserCmd_AlpineFallback(t *testing.T) {
	t.Parallel()
	cmd := buildCreateUserCmd("alice")
	// Alpine's adduser -D must be the last fallback.
	assert.Contains(t, cmd, "adduser -D alice", "Alpine adduser -D should be present")
	alpineIdx := strings.Index(cmd, "adduser -D")
	rhelIdx := strings.Index(cmd, "useradd -m")
	assert.Greater(t, alpineIdx, rhelIdx, "Alpine adduser -D should come after useradd")
}

func TestBuildCreateUserCmd_SuppressesStderr(t *testing.T) {
	t.Parallel()
	cmd := buildCreateUserCmd("bob")
	// Each alternative should redirect stderr to /dev/null so that the
	// fallback chain works correctly (non-zero exit triggers the next ||).
	assert.Contains(t, cmd, "2>/dev/null", "stderr should be suppressed")
}

func TestBuildCreateUserCmd_SpecialUsername(t *testing.T) {
	t.Parallel()
	cmd := buildCreateUserCmd("www-data")
	assert.Contains(t, cmd, "www-data")
}

// ─── buildAddToGroupCmd ───────────────────────────────────────────────────────

func TestBuildAddToGroupCmd_ContainsSudoGroup(t *testing.T) {
	t.Parallel()
	cmd := buildAddToGroupCmd("alice")
	assert.Contains(t, cmd, "sudo", "should add to sudo group (Debian/Ubuntu)")
}

func TestBuildAddToGroupCmd_ContainsWheelGroup(t *testing.T) {
	t.Parallel()
	cmd := buildAddToGroupCmd("alice")
	assert.Contains(t, cmd, "wheel", "should add to wheel group (RHEL/CentOS/Alpine)")
}

func TestBuildAddToGroupCmd_SudoBeforeWheel(t *testing.T) {
	t.Parallel()
	cmd := buildAddToGroupCmd("alice")
	sudoIdx := strings.Index(cmd, "sudo")
	wheelIdx := strings.Index(cmd, "wheel")
	assert.Less(t, sudoIdx, wheelIdx, "sudo group should be tried before wheel")
}

func TestBuildAddToGroupCmd_EndsWithTrue(t *testing.T) {
	t.Parallel()
	cmd := buildAddToGroupCmd("alice")
	// The command must end with `|| true` so it never fails even on distros
	// that have neither sudo nor wheel groups.
	assert.True(t, strings.HasSuffix(strings.TrimSpace(cmd), "|| true"),
		"command should end with '|| true' to be non-fatal")
}

func TestBuildAddToGroupCmd_ContainsUsername(t *testing.T) {
	t.Parallel()
	cmd := buildAddToGroupCmd("charlie")
	assert.Contains(t, cmd, "charlie")
}

// ─── buildRemovePasswordCmd ───────────────────────────────────────────────────

func TestBuildRemovePasswordCmd_ContainsUsername(t *testing.T) {
	t.Parallel()
	cmd := buildRemovePasswordCmd("alice")
	assert.Contains(t, cmd, "alice")
}

func TestBuildRemovePasswordCmd_UsesPasswdD(t *testing.T) {
	t.Parallel()
	cmd := buildRemovePasswordCmd("alice")
	assert.Contains(t, cmd, "passwd -d", "should use passwd -d to remove password")
}

func TestBuildRemovePasswordCmd_NonFatal(t *testing.T) {
	t.Parallel()
	cmd := buildRemovePasswordCmd("alice")
	// Must be non-fatal because passwd may not exist on minimal images.
	assert.True(t, strings.HasSuffix(strings.TrimSpace(cmd), "|| true"),
		"command should end with '|| true' to be non-fatal")
}

// ─── buildSudoersCmd ─────────────────────────────────────────────────────────

func TestBuildSudoersCmd_ContainsUsername(t *testing.T) {
	t.Parallel()
	cmd := buildSudoersCmd("alice")
	assert.Contains(t, cmd, "alice")
}

func TestBuildSudoersCmd_ContainsNopasswd(t *testing.T) {
	t.Parallel()
	cmd := buildSudoersCmd("alice")
	assert.Contains(t, cmd, "NOPASSWD: ALL", "sudoers entry should grant passwordless sudo")
}

func TestBuildSudoersCmd_IdempotentViaGrep(t *testing.T) {
	t.Parallel()
	cmd := buildSudoersCmd("alice")
	// The command must check for an existing entry before appending.
	assert.Contains(t, cmd, "grep -q", "should check for existing entry before appending")
}

func TestBuildSudoersCmd_TouchesSudoers(t *testing.T) {
	t.Parallel()
	cmd := buildSudoersCmd("alice")
	// Must ensure /etc/sudoers exists even on minimal images.
	assert.Contains(t, cmd, "touch /etc/sudoers")
}

func TestBuildSudoersCmd_AppendsEntry(t *testing.T) {
	t.Parallel()
	cmd := buildSudoersCmd("alice")
	assert.Contains(t, cmd, ">>", "should append to sudoers file")
	assert.Contains(t, cmd, "/etc/sudoers")
}

func TestBuildSudoersCmd_FullEntryFormat(t *testing.T) {
	t.Parallel()
	cmd := buildSudoersCmd("alice")
	// Verify the exact sudoers rule format.
	assert.Contains(t, cmd, "alice ALL=(ALL:ALL) NOPASSWD: ALL")
}
