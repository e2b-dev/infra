//go:build linux

package template

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// mustMapping packs the given runs into a compact Mapping. Offsets/lengths
// are in bytes and must be PageSize-aligned (the universal granularity).
func mustMapping(t *testing.T, runs []header.BuildMap) header.Mapping {
	t.Helper()

	m, err := header.NewMapping(header.PageSize, runs)
	require.NoError(t, err)

	return m
}

func TestMaxEntriesPerBlock(t *testing.T) {
	t.Parallel()

	const (
		page  = uint64(header.PageSize)
		block = uint64(header.HugepageSize)
	)
	buildA := uuid.New()
	buildB := uuid.New()

	tests := []struct {
		name string
		runs []header.BuildMap
		want int64
	}{
		{
			name: "empty mapping",
			runs: nil,
			want: 0,
		},
		{
			name: "single run covering one block",
			runs: []header.BuildMap{
				{Offset: 0, Length: block, BuildId: buildA},
			},
			want: 1,
		},
		{
			name: "single run spanning many blocks still counts once per block",
			runs: []header.BuildMap{
				{Offset: 0, Length: 4 * block, BuildId: buildA},
			},
			want: 1,
		},
		{
			name: "interleaved builds inside one block",
			runs: []header.BuildMap{
				{Offset: 0, Length: page, BuildId: buildA},
				{Offset: page, Length: page, BuildId: buildB},
				{Offset: 2 * page, Length: page, BuildId: buildA},
				{Offset: 3 * page, Length: block - 3*page, BuildId: buildB},
			},
			want: 4,
		},
		{
			name: "empty (uuid.Nil) runs are not counted",
			runs: []header.BuildMap{
				{Offset: 0, Length: page, BuildId: buildA},
				{Offset: page, Length: page, BuildId: uuid.Nil},
				{Offset: 2 * page, Length: block - 2*page, BuildId: buildB},
			},
			want: 2,
		},
		{
			name: "run spilling into the next block joins its count",
			runs: []header.BuildMap{
				// Block 0: A only. Block 1: A's tail + B + A = 3.
				{Offset: 0, Length: block + page, BuildId: buildA},
				{Offset: block + page, Length: page, BuildId: buildB},
				{Offset: block + 2*page, Length: block - 2*page, BuildId: buildA},
			},
			want: 3,
		},
		{
			name: "fragmented block after a coarse block wins",
			runs: []header.BuildMap{
				{Offset: 0, Length: block, BuildId: buildA},
				{Offset: block, Length: page, BuildId: buildA},
				{Offset: block + page, Length: page, BuildId: buildB},
				{Offset: block + 2*page, Length: block - 2*page, BuildId: buildA},
			},
			want: 3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			require.Equal(t, tt.want, maxEntriesPerBlock(mustMapping(t, tt.runs)))
		})
	}
}
