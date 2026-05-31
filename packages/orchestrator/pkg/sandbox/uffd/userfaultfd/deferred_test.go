//go:build linux

package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeferredFaultsDedupesByPage(t *testing.T) {
	t.Parallel()

	d := deferredFaults{pageSize: 4096}
	require.True(t, d.push(&UffdPagefault{address: 42}))
	require.False(t, d.push(&UffdPagefault{address: 43}))
	require.True(t, d.push(&UffdPagefault{address: 4096}))

	require.Len(t, d.drain(), 2)
	require.Empty(t, d.drain())

	require.True(t, d.push(&UffdPagefault{address: 42}))
	require.Len(t, d.drain(), 1)
}

func TestDeferredFaultsUpgradesReadToWrite(t *testing.T) {
	t.Parallel()

	var d deferredFaults
	require.True(t, d.push(&UffdPagefault{address: 42}))
	require.False(t, d.push(&UffdPagefault{address: 42, flags: UFFD_PAGEFAULT_FLAG_WRITE}))

	out := d.drain()
	require.Len(t, out, 1)
	require.NotZero(t, out[0].flags&UFFD_PAGEFAULT_FLAG_WRITE)
}
