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

// TestDeferredFaultsKeepsWPDistinctFromMissing is a regression test: a WP
// fault deferred for a page that already has a deferred MISSING entry must
// keep its own entry. Folding it into the MISSING one drops the WP flag, and
// the MISSING retry can short-circuit on an already-present page without
// waking — stranding the WP-blocked writer forever.
func TestDeferredFaultsKeepsWPDistinctFromMissing(t *testing.T) {
	t.Parallel()

	var d deferredFaults
	// MISSING (read) first, then a WP write fault for the same page.
	d.push(&UffdPagefault{address: 42})
	d.push(&UffdPagefault{address: 42, flags: UFFD_PAGEFAULT_FLAG_WP | UFFD_PAGEFAULT_FLAG_WRITE})

	out := d.drain()
	require.Len(t, out, 2, "WP and MISSING faults for the same page are distinct retries")

	var wp, missing *UffdPagefault
	for _, pf := range out {
		if pf.flags&UFFD_PAGEFAULT_FLAG_WP != 0 {
			wp = pf
		} else {
			missing = pf
		}
	}
	require.NotNil(t, wp, "the WP fault must survive the dedup")
	require.NotNil(t, missing, "the MISSING fault must survive the dedup")
	require.Zero(t, missing.flags&UFFD_PAGEFAULT_FLAG_WRITE,
		"the WP fault's WRITE flag must not leak into the MISSING entry")

	// Same-kind dedup still applies in both directions.
	d.push(&UffdPagefault{address: 42, flags: UFFD_PAGEFAULT_FLAG_WP | UFFD_PAGEFAULT_FLAG_WRITE})
	d.push(&UffdPagefault{address: 42, flags: UFFD_PAGEFAULT_FLAG_WP | UFFD_PAGEFAULT_FLAG_WRITE})
	d.push(&UffdPagefault{address: 42})
	require.Len(t, d.drain(), 2)
}
