//go:build linux

package sandbox

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/metadata"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const testBlockSize = 4096

// buildTestHeader assembles a header.Header at testBlockSize for the given
// own build ID and raw BuildMap entries (deliberately allowed to overlap,
// since real merged/dedup headers can have overlapping entries across
// layers).
func buildTestHeader(t *testing.T, own uuid.UUID, entries []header.BuildMap) *header.Header {
	t.Helper()

	mapping, err := header.NewMapping(testBlockSize, entries)
	require.NoError(t, err)

	return &header.Header{
		Metadata: &header.Metadata{
			BlockSize: testBlockSize,
			BuildId:   own,
		},
		Mapping: mapping,
	}
}

func TestBuildDiffMemoryPrefetchMapping(t *testing.T) {
	t.Parallel()

	own := uuid.New()
	other := uuid.New()

	t.Run("selects only own-build blocks and excludes other builds", func(t *testing.T) {
		t.Parallel()

		h := buildTestHeader(t, own, []header.BuildMap{
			{Offset: 0, Length: testBlockSize, BuildId: own},
			{Offset: 2 * testBlockSize, Length: testBlockSize, BuildId: other},
			{Offset: 3 * testBlockSize, Length: testBlockSize, BuildId: own},
		})

		got := buildDiffMemoryPrefetchMapping(h)

		require.NotNil(t, got)
		assert.Equal(t, []uint64{0, 3}, got.Indices, "only own-BuildId blocks, offset order")
		assert.Equal(t, int64(testBlockSize), got.BlockSize)
	})

	t.Run("dedups a block index covered by multiple own-build entries", func(t *testing.T) {
		t.Parallel()

		// Simulates a merged header where the same block offset is covered by
		// more than one BuildMap entry attributed to this build (e.g.
		// overlapping ranges folded in across layers): block index 1 is
		// covered by both own entries below and must be counted once. The
		// trailing base entry makes this a diff (layered on a base), not a full
		// image.
		h := buildTestHeader(t, own, []header.BuildMap{
			{Offset: 0, Length: 2 * testBlockSize, BuildId: own},             // blocks 0, 1
			{Offset: testBlockSize, Length: 2 * testBlockSize, BuildId: own}, // blocks 1, 2
			{Offset: 5 * testBlockSize, Length: testBlockSize, BuildId: other},
		})

		got := buildDiffMemoryPrefetchMapping(h)

		require.NotNil(t, got)
		assert.Equal(t, []uint64{0, 1, 2}, got.Indices, "each block index counted exactly once")
	})

	t.Run("full image with no base layer (base/build template) returns nil", func(t *testing.T) {
		t.Parallel()

		// Every block owned by this build's own ID and no distinct base layer:
		// a base/build template, not a pause diff. Prefetching all of it would
		// pull the whole template image, so it must be skipped.
		h := buildTestHeader(t, own, []header.BuildMap{
			{Offset: 0, Length: 3 * testBlockSize, BuildId: own},
		})

		assert.Nil(t, buildDiffMemoryPrefetchMapping(h))
	})

	t.Run("nil header returns nil", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, buildDiffMemoryPrefetchMapping(nil))
	})

	t.Run("nil metadata returns nil", func(t *testing.T) {
		t.Parallel()

		assert.Nil(t, buildDiffMemoryPrefetchMapping(&header.Header{Metadata: nil}))
	})

	t.Run("zero block size returns nil", func(t *testing.T) {
		t.Parallel()

		h := &header.Header{Metadata: &header.Metadata{BlockSize: 0, BuildId: own}}
		assert.Nil(t, buildDiffMemoryPrefetchMapping(h))
	})

	t.Run("empty diff (no own-build entries) returns nil", func(t *testing.T) {
		t.Parallel()

		h := buildTestHeader(t, own, []header.BuildMap{
			{Offset: 0, Length: testBlockSize, BuildId: other},
		})

		assert.Nil(t, buildDiffMemoryPrefetchMapping(h))
	})

	t.Run("empty mapping returns nil", func(t *testing.T) {
		t.Parallel()

		h := buildTestHeader(t, own, nil)
		assert.Nil(t, buildDiffMemoryPrefetchMapping(h))
	})
}

func TestSelectResumePrefetch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		source          string
		init, lastCycle bool
	}{
		{"off", false, false},
		{"init", true, false},
		{"last-cycle", false, true},
		{"both", true, true},
		{"", true, false},        // unknown -> init (default)
		{"garbage", true, false}, // unknown -> init (default)
	}
	for _, c := range cases {
		i, f := selectResumePrefetch(c.source)
		assert.Equalf(t, c.init, i, "useInit for source %q", c.source)
		assert.Equalf(t, c.lastCycle, f, "useLastCycle for source %q", c.source)
	}
}

func TestCapResumePrefetch(t *testing.T) {
	t.Parallel()

	const bs = int64(2 * 1024 * 1024)
	// 5 blocks == 10 MiB.
	m := &metadata.MemoryPrefetchMapping{Indices: []uint64{0, 1, 2, 3, 4}, BlockSize: bs}

	t.Run("uncapped passes through unchanged", func(t *testing.T) {
		t.Parallel()
		assert.Same(t, m, capResumePrefetch(m, -1))
	})

	t.Run("cap larger than the set passes through unchanged", func(t *testing.T) {
		t.Parallel()
		assert.Same(t, m, capResumePrefetch(m, 100))
	})

	t.Run("cap truncates to the earliest blocks in offset order", func(t *testing.T) {
		t.Parallel()
		got := capResumePrefetch(m, 6) // 6 MiB / 2 MiB = 3 blocks
		require.NotNil(t, got)
		assert.Equal(t, []uint64{0, 1, 2}, got.Indices)
		assert.Equal(t, bs, got.BlockSize)
		assert.Equal(t, []uint64{0, 1, 2, 3, 4}, m.Indices, "original mapping is not mutated")
	})

	t.Run("zero cap truncates to an empty set", func(t *testing.T) {
		t.Parallel()
		got := capResumePrefetch(m, 0)
		require.NotNil(t, got)
		assert.Empty(t, got.Indices, "0 MiB keeps no blocks; everything demand-faults")
		assert.Equal(t, bs, got.BlockSize)
	})

	t.Run("nil mapping stays nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, capResumePrefetch(nil, 4))
	})

	t.Run("unknown block size passes through unchanged", func(t *testing.T) {
		t.Parallel()
		z := &metadata.MemoryPrefetchMapping{Indices: []uint64{0, 1}, BlockSize: 0}
		assert.Same(t, z, capResumePrefetch(z, 1))
	})
}

// TestBuildDiffMemoryPrefetchMappingUnalignedEntry pins the fix for a dedup
// entry aligned to a smaller granule than Metadata.BlockSize that straddles a
// block boundary: enumerating by block index must record every block the entry
// covers, not just the first. BlockSize is 8192 while the own-build entry
// (offset 4096, length 8192) spans bytes 4096–12287 — blocks 0 and 1 — so
// byte-stepping from the unaligned offset would drop block 1.
func TestBuildDiffMemoryPrefetchMappingUnalignedEntry(t *testing.T) {
	t.Parallel()

	own := uuid.New()
	other := uuid.New()

	mapping, err := header.NewMapping(4096, []header.BuildMap{
		{Offset: 0, Length: 4096, BuildId: other}, // base layer, so this reads as a diff
		{Offset: 4096, Length: 8192, BuildId: own},
	})
	require.NoError(t, err)

	h := &header.Header{
		Metadata: &header.Metadata{BlockSize: 8192, BuildId: own},
		Mapping:  mapping,
	}

	got := buildDiffMemoryPrefetchMapping(h)
	require.NotNil(t, got)
	assert.Equal(t, []uint64{0, 1}, got.Indices, "the tail block of a cross-boundary entry must not be dropped")
	assert.Equal(t, int64(8192), got.BlockSize)
}
