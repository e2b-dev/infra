//go:build linux

package fc

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
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

func TestPidFDRemainsOpenAfterWait(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	cmd, pidFD := startPidFDCommand(t, ctx, "true")
	require.NoError(t, cmd.Wait())
	require.False(t, fdClosed(pidFD))

	p := &Process{pidFD: pidFD}
	require.NoError(t, p.closePidFD())
	require.True(t, fdClosed(pidFD))
}

func TestProcessStopClosesPidFD(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	cmd, pidFD := startPidFDCommand(t, ctx, "sleep", "60")
	pid := cmd.Process.Pid

	exit := utils.NewErrorOnce()
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_ = cmd.Wait()
		exit.SetError(nil)
	}()
	t.Cleanup(func() {
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-waitDone
	})

	p := &Process{
		cmd:         cmd,
		pidFD:       pidFD,
		Exit:        exit,
		metricsPath: t.TempDir() + "/metrics.fifo",
	}
	require.NoError(t, p.Stop(ctx))
	require.Equal(t, -1, p.pidFD)
	require.True(t, fdClosed(pidFD))
	require.NoError(t, p.Stop(ctx))
}

func TestProcessStopDoesNotSignalProcessGroupAfterLeaderExit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()

	childPidPath := filepath.Join(t.TempDir(), "child.pid")
	cmd, pidFD := startPidFDCommand(t, ctx, "bash", "-c", "sleep 60 & echo $! > \"$1\"", "sh", childPidPath)
	pid := cmd.Process.Pid

	exit := utils.NewErrorOnce()
	waitDone := make(chan struct{})
	go func() {
		defer close(waitDone)
		_ = cmd.Wait()
		exit.SetError(nil)
	}()

	var childPid int
	require.Eventually(t, func() bool {
		data, err := os.ReadFile(childPidPath)
		if err != nil {
			return false
		}

		childPid, err = strconv.Atoi(strings.TrimSpace(string(data)))

		return err == nil && childPid > 0
	}, time.Second, 10*time.Millisecond)
	t.Cleanup(func() {
		_ = syscall.Kill(childPid, syscall.SIGKILL)
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		<-waitDone
	})

	require.Eventually(t, func() bool {
		select {
		case <-waitDone:
			return true
		default:
			return false
		}
	}, time.Second, 10*time.Millisecond)

	p := &Process{
		cmd:         cmd,
		pidFD:       pidFD,
		Exit:        exit,
		files:       &storage.SandboxFiles{SandboxID: "test"},
		metricsPath: filepath.Join(t.TempDir(), "metrics.fifo"),
	}
	require.NoError(t, p.Stop(ctx))
	require.NoError(t, syscall.Kill(childPid, 0))
}

func startPidFDCommand(t *testing.T, ctx context.Context, name string, args ...string) (*exec.Cmd, int) {
	t.Helper()

	pidFD := -1
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, PidFD: &pidFD}
	require.NoError(t, cmd.Start())
	if pidFD < 0 {
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		_ = cmd.Wait()
		t.Skip("kernel does not support pidfd")
	}

	return cmd, pidFD
}

func fdClosed(fd int) bool {
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fd), uintptr(syscall.F_GETFD), 0)

	return errno == syscall.EBADF
}
