//go:build linux

package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseSmapsRollup(t *testing.T) {
	input := []byte(`7f19d8000000-7f19d8200000 ---p 00000000 00:00 0 [rollup]
Rss:                10240 kB
Pss:                 6144 kB
Shared_Clean:        2048 kB
Shared_Dirty:        1024 kB
Private_Clean:       4096 kB
Private_Dirty:       3072 kB
Referenced:          8192 kB
Anonymous:           4096 kB
LazyFree:             128 kB
AnonHugePages:       2048 kB
Shared_Hugetlb:       512 kB
Private_Hugetlb:      256 kB
Swap:                  64 kB
SwapPss:               32 kB
Locked:                16 kB
`)

	rollup, err := parseSmapsRollup(input)
	require.NoError(t, err)

	require.EqualValues(t, 10240*1024, rollup.RSSBytes)
	require.EqualValues(t, 6144*1024, rollup.PSSBytes)
	require.EqualValues(t, 3584*1024, rollup.SharedRSSBytes())
	require.EqualValues(t, 7424*1024, rollup.PrivateRSSBytes())
	require.EqualValues(t, 4096*1024, rollup.DirtyRSSBytes())
	require.InDelta(t, 0.4, rollup.DirtyRSSRatio(), 0.000001)
	require.EqualValues(t, 2048*1024, rollup.AnonHugePagesBytes)
	require.EqualValues(t, 64*1024, rollup.SwapBytes)
}

func TestParseSmapsRollupRejectsBadNumbers(t *testing.T) {
	_, err := parseSmapsRollup([]byte("Rss: nope kB\n"))
	require.Error(t, err)
}
