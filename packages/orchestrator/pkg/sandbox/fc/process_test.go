//go:build linux

package fc

import (
	"context"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProcessGroupExistsForSetsidChild(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "sleep", "60")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	require.NoError(t, cmd.Start())

	pid := cmd.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		_ = cmd.Wait()
	})

	require.True(t, processGroupExists(pid))
	require.NoError(t, signalProcessGroup(pid, syscall.SIGKILL))
	require.Error(t, cmd.Wait())
	require.Eventually(t, func() bool {
		return !processGroupExists(pid)
	}, time.Second, 10*time.Millisecond)
}
