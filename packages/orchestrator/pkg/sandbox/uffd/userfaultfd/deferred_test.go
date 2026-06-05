//go:build linux

package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeferredFaultsDedupesByAddress(t *testing.T) {
	t.Parallel()

	var d deferredFaults
	d.push(&UffdPagefault{address: 42})
	d.push(&UffdPagefault{address: 42})
	d.push(&UffdPagefault{address: 43})

	require.Len(t, d.drain(), 2)
	require.Empty(t, d.drain())

	d.push(&UffdPagefault{address: 42})
	require.Len(t, d.drain(), 1)
}

func TestDeferredFaultsUpgradesReadToWrite(t *testing.T) {
	t.Parallel()

	var d deferredFaults
	d.push(&UffdPagefault{address: 42})
	d.push(&UffdPagefault{address: 42, flags: UFFD_PAGEFAULT_FLAG_WRITE})

	out := d.drain()
	require.Len(t, out, 1)
	require.NotZero(t, out[0].flags&UFFD_PAGEFAULT_FLAG_WRITE)
}
