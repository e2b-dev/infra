//go:build linux

package factories

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const orchestratorLockHelperEnv = "E2B_ORCHESTRATOR_LOCK_HELPER"

func TestOrchestratorLockHelperProcess(t *testing.T) {
	if os.Getenv(orchestratorLockHelperEnv) != "1" {
		return
	}

	path := os.Getenv("E2B_ORCHESTRATOR_LOCK_PATH")
	reclaimMarker := os.Getenv("E2B_ORCHESTRATOR_RECLAIM_MARKER")
	lock, err := acquireOrchestratorLock(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(23)
	}
	defer func() { _ = releaseOrchestratorLock(lock) }()

	// This marker represents the first destructive startup-reclaim action.
	// A losing process must exit before it can reach this point.
	if reclaimMarker != "" {
		if err := os.WriteFile(reclaimMarker, []byte("reclaim-started\n"), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(24)
		}
	}
	fmt.Println("locked")
	_ = os.Stdout.Sync()

	for {
		time.Sleep(time.Hour)
	}
}

func startOrchestratorLockHelper(t *testing.T, path, reclaimMarker string) (*exec.Cmd, *bufio.Reader) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestOrchestratorLockHelperProcess$")
	cmd.Env = append(os.Environ(),
		orchestratorLockHelperEnv+"=1",
		"E2B_ORCHESTRATOR_LOCK_PATH="+path,
		"E2B_ORCHESTRATOR_RECLAIM_MARKER="+reclaimMarker,
	)
	stdout, err := cmd.StdoutPipe()
	require.NoError(t, err)
	cmd.Stderr = os.Stderr
	require.NoError(t, cmd.Start())
	t.Cleanup(func() {
		if cmd.ProcessState == nil || !cmd.ProcessState.Exited() {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	})
	return cmd, bufio.NewReader(stdout)
}

func lockInode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	stat, ok := info.Sys().(*syscall.Stat_t)
	require.True(t, ok)
	return stat.Ino
}

func TestOrchestratorLockKeepsStableInodeAcrossOwners(t *testing.T) {
	t.Parallel()

	path := filepath.Join(t.TempDir(), "orchestrator.lock")
	first, err := acquireOrchestratorLock(path)
	require.NoError(t, err)
	firstInode := lockInode(t, path)

	_, err = acquireOrchestratorLock(path)
	require.ErrorContains(t, err, "another instance is running")

	require.NoError(t, releaseOrchestratorLock(first))
	require.FileExists(t, path)
	require.Equal(t, firstInode, lockInode(t, path))

	second, err := acquireOrchestratorLock(path)
	require.NoError(t, err)
	require.Equal(t, firstInode, lockInode(t, path))

	// A third contender must still conflict with the second owner. If the
	// first owner had unlinked after closing, this third lock could silently
	// attach to a new inode while the second remained live on the old one.
	_, err = acquireOrchestratorLock(path)
	require.ErrorContains(t, err, "another instance is running")

	require.NoError(t, releaseOrchestratorLock(second))
	third, err := acquireOrchestratorLock(path)
	require.NoError(t, err)
	require.Equal(t, firstInode, lockInode(t, path))
	require.NoError(t, releaseOrchestratorLock(third))
}

func TestDeployedDevRequiresOrchestratorLock(t *testing.T) {
	t.Parallel()
	require.True(t, requiresOrchestratorLock("dev", true), "deployed dev must lock")
	require.True(t, requiresOrchestratorLock("prod", true), "production must lock")
	require.False(t, requiresOrchestratorLock("local", true), "explicit local mode is the sole exemption")
	require.False(t, requiresOrchestratorLock("dev", false), "non-runtime services do not touch host namespaces")
}

func TestOrchestratorLockIsCrossProcessCrashSafeAndPrecedesReclaim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "orchestrator.lock")
	ownerMarker := filepath.Join(dir, "owner-reclaim")
	owner, ownerOutput := startOrchestratorLockHelper(t, path, ownerMarker)
	line, err := ownerOutput.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "locked\n", line)
	require.FileExists(t, ownerMarker)
	lockedInode := lockInode(t, path)

	loserMarker := filepath.Join(dir, "loser-reclaim")
	loser, _ := startOrchestratorLockHelper(t, path, loserMarker)
	err = loser.Wait()
	var exitErr *exec.ExitError
	require.ErrorAs(t, err, &exitErr)
	require.Equal(t, 23, exitErr.ExitCode())
	require.NoFileExists(t, loserMarker, "a lock loser reached startup reclaim")

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, strconv.Itoa(owner.Process.Pid), string(data[:len(data)-1]))

	require.NoError(t, owner.Process.Kill())
	err = owner.Wait()
	require.Error(t, err)
	require.Equal(t, lockedInode, lockInode(t, path), "SIGKILL must not replace the lock inode")

	replacementMarker := filepath.Join(dir, "replacement-reclaim")
	replacement, replacementOutput := startOrchestratorLockHelper(t, path, replacementMarker)
	line, err = replacementOutput.ReadString('\n')
	require.NoError(t, err)
	require.Equal(t, "locked\n", line)
	require.FileExists(t, replacementMarker)
	require.Equal(t, lockedInode, lockInode(t, path))
	require.NoError(t, replacement.Process.Kill())
	_ = replacement.Wait()
}
