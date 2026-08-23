//go:build linux

package cgroups

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

const (
	oneByte  = 1
	kilobyte = 1024 * oneByte
	megabyte = 1024 * kilobyte
)

func TestNewCgroup2Manager_NonCgroup2FS(t *testing.T) {
	t.Parallel()

	tmpDir := t.TempDir()
	_, err := NewCgroup2Manager(WithCgroup2RootSysFSPath(tmpDir))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "not a cgroup2 filesystem")
}

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

func TestFreezeUnfreeze(t *testing.T) {
	t.Parallel()

	if os.Geteuid() != 0 {
		t.Skip("must run as root")

		return
	}

	cgroupPath := createCgroupPath(t, "freeze-thaw")

	m, err := NewCgroup2Manager(
		WithCgroup2ProcessType(ProcessTypeUser, cgroupPath, map[string]string{}),
	)
	require.NoError(t, err)

	t.Cleanup(func() {
		err := m.Close()
		assert.NoError(t, err)
	})

	fullPath := m.cgroupPaths[ProcessTypeUser]
	readFreeze := func() string {
		data, err := os.ReadFile(fullPath + "/cgroup.freeze")
		require.NoError(t, err)

		return string(data)
	}

	// Initially thawed.
	assert.Equal(t, "0\n", readFreeze())

	// Freeze.
	err = m.Freeze(ProcessTypeUser)
	require.NoError(t, err)
	assert.Equal(t, "1\n", readFreeze())

	// Freeze again (idempotent).
	err = m.Freeze(ProcessTypeUser)
	require.NoError(t, err)
	assert.Equal(t, "1\n", readFreeze())

	// Unfreeze.
	err = m.Unfreeze(ProcessTypeUser)
	require.NoError(t, err)
	assert.Equal(t, "0\n", readFreeze())

	// Unfreeze again (idempotent).
	err = m.Unfreeze(ProcessTypeUser)
	require.NoError(t, err)
	assert.Equal(t, "0\n", readFreeze())

	// Unknown process type.
	err = m.Freeze("unknown")
	require.Error(t, err)
	err = m.Unfreeze("unknown")
	require.Error(t, err)
}

func createCgroupPath(t *testing.T, s string) string {
	t.Helper()

	randPart := rand.Int()

	return fmt.Sprintf("envd-test-%s-%d", s, randPart)
}

func startProcess(t *testing.T, m *Cgroup2Manager, pt ProcessType) *exec.Cmd {
	t.Helper()

	cmdName, args := "bash", []string{"-c", `sleep 1 && exec perl -e 'my $x = "A" x (512*1024*1024); sleep 300'`}
	cmd := exec.CommandContext(t.Context(), cmdName, args...)

	fd, ok := m.GetFileDescriptor(pt)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		UseCgroupFD: ok,
		CgroupFD:    fd,
		Setpgid:     true,
	}

	err := cmd.Start()
	require.NoError(t, err)

	t.Cleanup(func() { killProcessGroup(cmd) })

	return cmd
}

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}

	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
		_ = cmd.Process.Kill()
	}
}

func waitForProcess(t *testing.T, cmd *exec.Cmd, timeout time.Duration) error {
	t.Helper()

	done := make(chan error, 1)

	go func() {
		defer close(done)
		done <- cmd.Wait()
	}()

	ctx, cancel := context.WithTimeout(t.Context(), timeout)
	defer cancel()

	select {
	case <-ctx.Done():
		killProcessGroup(cmd)
		<-done

		return ctx.Err()
	case err := <-done:
		return err
	}
}

// TestCgroup2Manager_Frozen covers the one real Frozen implementation: the cgroup.events
// parser. The noop manager, the non-Linux stub and the test fakes all return canned values,
// so nothing else exercises this text handling — replacing the body with `return false, nil`
// passes the rest of the suite while making every pre-pause freeze report notFrozen and burn
// its whole wait budget in production.
//
// No root needed: cgroupPaths is just a map, so a temp dir with a synthetic cgroup.events is
// enough.
func TestCgroup2Manager_Frozen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		events  string
		want    bool
		wantErr bool
	}{
		{name: "frozen", events: "populated 1\nfrozen 1\n", want: true},
		{name: "not frozen", events: "populated 1\nfrozen 0\n", want: false},
		{name: "frozen key first", events: "frozen 1\npopulated 0\n", want: true},
		// A cgroup that has never been frozen may omit the key entirely; that is not an
		// error, it is simply not frozen.
		{name: "key absent", events: "populated 1\n", want: false},
		{name: "empty file", events: "", want: false},
		// The kernel writes "key value\n", but a parser that depends on the trailing
		// newline or on exact spacing would be brittle for no reason.
		{name: "no trailing newline", events: "populated 1\nfrozen 1", want: true},
		{name: "surrounding whitespace", events: "  frozen 1  \n", want: true},
		// Only an exact key match counts: a longer key that merely starts with "frozen"
		// must not be read as the freeze state.
		{name: "similar key is not a match", events: "frozen_time_usec 12345\n", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(dir, "cgroup.events"), []byte(tc.events), 0o644))
			mgr := Cgroup2Manager{cgroupPaths: map[ProcessType]string{ProcessTypeUser: dir}}

			got, err := mgr.Frozen(ProcessTypeUser)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}

	t.Run("unknown process type errors", func(t *testing.T) {
		t.Parallel()

		mgr := Cgroup2Manager{cgroupPaths: map[ProcessType]string{}}
		_, err := mgr.Frozen(ProcessTypeUser)
		require.Error(t, err)
	})

	t.Run("missing cgroup.events errors", func(t *testing.T) {
		t.Parallel()

		mgr := Cgroup2Manager{cgroupPaths: map[ProcessType]string{ProcessTypeUser: t.TempDir()}}
		_, err := mgr.Frozen(ProcessTypeUser)
		require.Error(t, err, "a cgroup that vanished mid-sweep must surface as an error, not as not-frozen")
	})
}
