package handler

import (
	"context"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const oneByte = 1
const kilobyte = 1024 * oneByte

func TestCgroupRoundTrip(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("must run as root")
		return
	}

	maxMemory := int64(100 * kilobyte)
	maxTimeout := time.Second * 5

	t.Run("process does not die without cgroups", func(t *testing.T) {
		t.Parallel()

		// create new child process
		cmd := startProcess(t)

		// wait for child process to die
		err := waitForProcess(t, cmd, maxTimeout)

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("process dies with cgroups", func(t *testing.T) {
		t.Parallel()

		// find current process' cgroup
		cgroupOfParent := "envdcommands.slice" //getTestCgroupName(t)

		// create a child cgroup with a low memory limit
		cgroupOfTest := "test-commands.service"

		// create manager
		m := NewCGroupManager(cgroupOfParent, cgroupOfTest, &cgroup2.Resources{
			Memory: &cgroup2.Memory{
				Swap: utils.ToPtr(int64(0)),
				High: utils.ToPtr(int64(float64(maxMemory) * 0.8)),
				Max:  &maxMemory,
			},
		})
		require.NotNil(t, m)
		require.NotNil(t, m.mgr)

		// create new child process
		cmd := startProcess(t)

		// put child process in the child cgroup
		err := m.Assign(cmd.Process.Pid)
		require.NoError(t, err)

		// wait for child process to die
		err = waitForProcess(t, cmd, maxTimeout)

		// verify process exited correctly
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, "signal: killed", exitErr.Error())
		assert.False(t, exitErr.Exited())
		assert.False(t, exitErr.Success())
		assert.Equal(t, -1, exitErr.ExitCode())

		// dig a little deeper
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		require.True(t, ok)
		assert.Equal(t, syscall.SIGKILL, ws.Signal())
		assert.Equal(t, true, ws.Signaled())
		assert.Equal(t, false, ws.Stopped())
		assert.Equal(t, false, ws.Continued())
		assert.Equal(t, false, ws.CoreDump())
		assert.Equal(t, false, ws.Exited())
		assert.Equal(t, -1, ws.ExitStatus())
	})
}

func startProcess(t *testing.T) *exec.Cmd {
	t.Helper()

	cmdName, args := "bash", []string{"-c", `sleep 1 && tail /dev/zero`}
	cmd := exec.CommandContext(t.Context(), cmdName, args...)
	err := cmd.Start()
	require.NoError(t, err)

	return cmd
}

func waitForProcess(t *testing.T, cmd *exec.Cmd, timeout time.Duration) error {
	t.Helper()

	done := make(chan error)

	go func() {
		defer close(done)
		done <- cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	t.Cleanup(cancel)

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-done:
		return err
	}
}
