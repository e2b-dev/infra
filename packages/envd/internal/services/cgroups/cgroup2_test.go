package cgroups

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	oneByte  = 1
	kilobyte = 1024 * oneByte
	megabyte = 1024 * kilobyte
)

func TestCgroupRoundTrip(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("must run as root")

		return
	}

	maxTimeout := time.Second * 5

	t.Run("process does not die without cgroups", func(t *testing.T) {
		t.Parallel()

		// create manager
		m, err := NewCgroup2Manager()
		require.NoError(t, err)

		// create new child process
		cmd := startProcess(t, m, "not-a-real-one")

		// wait for child process to die
		err = waitForProcess(t, cmd, maxTimeout)

		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("process dies with cgroups", func(t *testing.T) {
		t.Parallel()

		cgroupPath := createCgroupPath(t, "real-one")

		// create manager
		m, err := NewCgroup2Manager(
			WithCgroup2ProcessType(ProcessTypePTY, cgroupPath, map[string]string{
				"memory.max": strconv.Itoa(1 * megabyte),
			}),
		)
		require.NoError(t, err)

		t.Cleanup(func() {
			err := m.Close()
			assert.NoError(t, err)
		})

		// create new child process
		cmd := startProcess(t, m, ProcessTypePTY)

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
		assert.True(t, ws.Signaled())
		assert.False(t, ws.Stopped())
		assert.False(t, ws.Continued())
		assert.False(t, ws.CoreDump())
		assert.False(t, ws.Exited())
		assert.Equal(t, -1, ws.ExitStatus())
	})

	t.Run("process cannot be spawned because memory limit is too low", func(t *testing.T) {
		t.Parallel()

		cgroupPath := createCgroupPath(t, "real-one")

		// create manager
		m, err := NewCgroup2Manager(
			WithCgroup2ProcessType(ProcessTypeSocat, cgroupPath, map[string]string{
				"memory.max": strconv.Itoa(1 * kilobyte),
			}),
		)
		require.NoError(t, err)

		t.Cleanup(func() {
			err := m.Close()
			assert.NoError(t, err)
		})

		// create new child process
		cmd := startProcess(t, m, ProcessTypeSocat)

		// wait for child process to die
		err = waitForProcess(t, cmd, maxTimeout)

		// verify process exited correctly
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, "exit status 253", exitErr.Error())
		assert.True(t, exitErr.Exited())
		assert.False(t, exitErr.Success())
		assert.Equal(t, 253, exitErr.ExitCode())

		// dig a little deeper
		ws, ok := exitErr.Sys().(syscall.WaitStatus)
		require.True(t, ok)
		assert.Equal(t, syscall.Signal(-1), ws.Signal())
		assert.False(t, ws.Signaled())
		assert.False(t, ws.Stopped())
		assert.False(t, ws.Continued())
		assert.False(t, ws.CoreDump())
		assert.True(t, ws.Exited())
		assert.Equal(t, 253, ws.ExitStatus())
	})
}

func createCgroupPath(t *testing.T, s string) string {
	t.Helper()

	randPart := rand.Int()

	return fmt.Sprintf("envd-test-%s-%d", s, randPart)
}

func startProcess(t *testing.T, m *Cgroup2Manager, pt ProcessType) *exec.Cmd {
	t.Helper()

	cmdName, args := "bash", []string{"-c", `sleep 1 && tail /dev/zero`}
	cmd := exec.CommandContext(t.Context(), cmdName, args...)

	fd, ok := m.GetFileDescriptor(pt)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: ok,
		CgroupFD:    fd,
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
