package fc

import (
	"os/exec"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHasProcessExited(t *testing.T) {
	t.Run("process has not exited", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "sleep", "infinity")

		// start the process
		err := cmd.Start()
		require.NoError(t, err)
		t.Cleanup(func() {
			err = cmd.Process.Kill()
			assert.NoError(t, err)
		})

		// verify that it has not ended
		isExited := hasProcessExited(cmd)
		assert.False(t, isExited)
	})

	t.Run("process has exited successfully", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "bash", "-c", "exit 0")

		// start the process
		err := cmd.Start()
		require.NoError(t, err)

		// wait for exit
		err = cmd.Wait()
		require.NoError(t, err)

		// verify that it has exited
		isExited := hasProcessExited(cmd)
		assert.True(t, isExited)
	})

	t.Run("process has exited with failure", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "bash", "-c", "exit 1")

		// start the process
		err := cmd.Start()
		require.NoError(t, err)

		// wait for exit
		err = cmd.Wait()
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, 1, exitErr.ExitCode())

		// verify that it has exited
		isExited := hasProcessExited(cmd)
		assert.True(t, isExited)
	})

	t.Run("process is nil", func(t *testing.T) {
		isExited := hasProcessExited(nil)
		assert.True(t, isExited)
	})

	t.Run("process has exited via signal", func(t *testing.T) {
		cmd := exec.CommandContext(t.Context(), "sleep", "infinity")

		// start the process
		err := cmd.Start()
		require.NoError(t, err)

		err = cmd.Process.Signal(syscall.SIGTERM)
		require.NoError(t, err)

		err = cmd.Wait()
		var exitErr *exec.ExitError
		require.ErrorAs(t, err, &exitErr)
		assert.Equal(t, -1, exitErr.ExitCode())
		waitStatus, ok := exitErr.ProcessState.Sys().(syscall.WaitStatus)
		require.True(t, ok, "ProcessState.Sys() should be of type syscall.WaitStatus")
		assert.True(t, waitStatus.Signaled())
		assert.Equal(t, syscall.SIGTERM, waitStatus.Signal())

		// verify that it has not ended
		isExited := hasProcessExited(cmd)
		assert.True(t, isExited)
	})
}
