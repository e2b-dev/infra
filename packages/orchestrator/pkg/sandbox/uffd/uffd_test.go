package uffd

import (
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// TestSetCloexec covers UFFD audit finding #4. The uffd fd we receive from
// Firecracker via ParseUnixRights does not have FD_CLOEXEC by default
// (we don't pass MSG_CMSG_CLOEXEC at the recvmsg site), so a later
// fork/exec in the orchestrator could leak it into a child. Verify the
// helper that production calls right after the parse actually sets the
// flag on a freshly-created fd that starts without it.
func TestSetCloexec(t *testing.T) {
	t.Parallel()

	var p [2]int
	require.NoError(t, syscall.Pipe(p[:]))
	t.Cleanup(func() {
		_ = syscall.Close(p[0])
		_ = syscall.Close(p[1])
	})

	flags, err := unix.FcntlInt(uintptr(p[0]), unix.F_GETFD, 0)
	require.NoError(t, err)
	require.Zerof(t, flags&unix.FD_CLOEXEC, "syscall.Pipe fd must start without FD_CLOEXEC; got flags=%#x", flags)

	require.NoError(t, setCloexec(p[0]))

	flags, err = unix.FcntlInt(uintptr(p[0]), unix.F_GETFD, 0)
	require.NoError(t, err)
	assert.NotZerof(t, flags&unix.FD_CLOEXEC, "setCloexec must set FD_CLOEXEC; got flags=%#x", flags)
}
