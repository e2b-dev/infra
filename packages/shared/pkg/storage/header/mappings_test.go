package header

import (
	"testing"
	"unsafe"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMappings_RoundTripFromSlice(t *testing.T) {
	t.Parallel()

	id1 := uuid.New()
	id2 := uuid.New()
	in := []BuildMap{
		{Offset: 0, Length: 4096, BuildId: id1, BuildStorageOffset: 0},
		{Offset: 4096, Length: 4096, BuildId: id2, BuildStorageOffset: 7},
		{Offset: 8192, Length: 4096, BuildId: id1, BuildStorageOffset: 4096},
	}

	m := MappingsFromSlice(in)

	require.Equal(t, len(in), m.Len())
	for i, want := range in {
		assert.Equal(t, want, m.At(i))
		assert.Equal(t, want.Offset, m.OffsetAt(i))
	}
	assert.Equal(t, in, m.Slice())

	// Dictionary contains only the two distinct builds.
	assert.ElementsMatch(t, []uuid.UUID{id1, id2}, m.Builds())

	// All() yields the same values.
	var rebuilt []BuildMap
	for _, b := range m.All() {
		rebuilt = append(rebuilt, b)
	}
	assert.Equal(t, in, rebuilt)
}

func TestMappings_Empty(t *testing.T) {
	t.Parallel()

	m := MappingsFromSlice(nil)
	assert.Equal(t, 0, m.Len())
	assert.Empty(t, m.Builds())
	assert.Nil(t, m.Slice())
}

// BenchmarkMappings_FromSlice exercises the construction path. The whole
// point of the dictionary is to retain less memory once the result lives
// in the template cache, not to be allocation-free at build time.
func BenchmarkMappings_FromSlice(b *testing.B) {
	id1 := uuid.New()
	id2 := uuid.New()
	in := make([]BuildMap, 10_000)
	for i := range in {
		id := id1
		if i%2 == 0 {
			id = id2
		}
		in[i] = BuildMap{
			Offset: uint64(i * 4096), Length: 4096,
			BuildId: id, BuildStorageOffset: uint64(i * 4096),
		}
	}

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = MappingsFromSlice(in)
	}
}

// TestMappings_EntrySize documents the per-entry footprint vs the legacy
// []BuildMap layout (40 bytes). Failure means the SoA struct grew or the
// platform changed BuildMap's layout — both are worth reviewing.
func TestMappings_EntrySize(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 40, int(unsafe.Sizeof(BuildMap{})))
	assert.Equal(t, 26, Mappings{}.entrySize())
}
