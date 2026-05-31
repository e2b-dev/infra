//go:build linux

package userfaultfd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestDeferredFaultsDedupesByPage(t *testing.T) {
	t.Parallel()

	d := deferredFaults{pageSize: 4096}
	d.push(&UffdPagefault{address: 42})
	d.push(&UffdPagefault{address: 43})
	d.push(&UffdPagefault{address: 4096})

	require.Len(t, d.drain(), 2)
	require.Empty(t, d.drain())

	d.push(&UffdPagefault{address: 42})
	require.Len(t, d.drain(), 1)
}
