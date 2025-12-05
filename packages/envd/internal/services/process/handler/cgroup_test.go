package handler

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

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
		cmd := startProcess(t, nil)

		// wait for child process to die
		err := waitForProcess(t, cmd, maxTimeout)

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("process dies with cgroups", func(t *testing.T) {
		t.Parallel()

		// find current process' cgroup
		cgroupOfParent := fmt.Sprintf("envdtests%d.slice", rand.Int())

		// create manager
		cgroupPath := filepath.Join("/sys/fs/cgroup", cgroupOfParent)
		m := NewCGroupManager(
			cgroupPath,
			map[string]string{"memory.max": strconv.FormatInt(maxMemory, 10)},
		)
		require.NotNil(t, m)
		require.Positive(t, m.cgroupFD)

		t.Cleanup(func() {
			err := os.Remove(cgroupPath)
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
		assert.Equal(t, true, ws.Signaled())
		assert.Equal(t, false, ws.Stopped())
		assert.Equal(t, false, ws.Continued())
		assert.Equal(t, false, ws.CoreDump())
		assert.Equal(t, false, ws.Exited())
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
