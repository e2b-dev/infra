//go:build linux

package network

import (
	"errors"
	"fmt"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

type fakeNotExistError struct {
	notExist bool
}

func (e fakeNotExistError) Error() string {
	return "fake iptables error"
}

func (e fakeNotExistError) IsNotExist() bool {
	return e.notExist
}

func TestIgnoreExpectedAbsentHandlesWrappedAndJoinedErrors(t *testing.T) {
	t.Parallel()

	wrapped := fmt.Errorf("wrapped: %w", fakeNotExistError{notExist: true})
	joined := errors.Join(wrapped, fakeNotExistError{notExist: true})

	require.True(t, ignoreExpectedAbsent(joined, isIPTablesNotExist))
	require.False(t, ignoreExpectedAbsent(errors.Join(joined, errors.New("boom")), isIPTablesNotExist))
	require.False(t, ignoreExpectedAbsent(fakeNotExistError{notExist: false}, isIPTablesNotExist))
}

func TestExpectedAbsentClassifiers(t *testing.T) {
	t.Parallel()

	require.True(t, isRouteNotExist(fmt.Errorf("route delete failed: %w", unix.ESRCH)))
	require.False(t, isRouteNotExist(unix.EPERM))

	require.True(t, isLinkNotExist(fmt.Errorf("link delete failed: %w", unix.ENODEV)))
	require.False(t, isLinkNotExist(unix.EPERM))

	require.True(t, isNamespaceNotExist(&os.PathError{Op: "remove", Path: "/var/run/netns/missing", Err: unix.ENOENT}))
	require.False(t, isNamespaceNotExist(unix.EPERM))
}
