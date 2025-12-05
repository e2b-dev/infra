package handler

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/containerd/cgroups/v3/cgroup2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	oneByte  = 1
	kilobyte = 1024 * oneByte
)

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
		cmd := startProcess(t, nil)

		// wait for child process to die
		err := waitForProcess(t, cmd, maxTimeout)

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("process dies with cgroups", func(t *testing.T) {
		t.Parallel()

		// find current process' cgroup
		cgroupOfParent := fmt.Sprintf("envdtests%d.slice", rand.Int())
		cgroupOfCommands := "commands.slice"

		// create manager
		m := NewCGroupManager(
			cgroupOfParent,
			cgroupOfCommands,
			&cgroup2.Resources{
				Memory: &cgroup2.Memory{
					Max: utils.ToPtr(maxMemory),
				},
			},
		)
		require.NotNil(t, m)
		require.Positive(t, m.cgroupFD)

		t.Cleanup(func() {
			cgroupParentPath := filepath.Join("/sys/fs/cgroup", cgroupOfParent)
			cgroupPath := filepath.Join(cgroupParentPath, cgroupOfCommands)

			err := os.Remove(cgroupPath)
			assert.NoError(t, err)

			err = os.Remove(cgroupParentPath)
			assert.NoError(t, err)

			err = m.Close()
			assert.NoError(t, err)
		})

		// create new child process
		cmd := startProcess(t, &m.cgroupFD)

		// wait for child process to die
		err := waitForProcess(t, cmd, maxTimeout)

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
		assert.True(t, ws.Signaled())
		assert.False(t, ws.Stopped())
		assert.False(t, ws.Continued())
		assert.False(t, ws.CoreDump())
		assert.False(t, ws.Exited())
		assert.Equal(t, -1, ws.ExitStatus())
	})
}

func startProcess(t *testing.T, cgroupFD *int) *exec.Cmd {
	t.Helper()

	cmdName, args := "bash", []string{"-c", `sleep 1 && tail /dev/zero`}
	cmd := exec.CommandContext(t.Context(), cmdName, args...)

	if cgroupFD != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{
			UseCgroupFD: true,
			CgroupFD:    *cgroupFD,
		}
	}
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
