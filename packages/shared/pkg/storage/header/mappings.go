package header

import (
	"fmt"
	"iter"
	"math"

	"github.com/google/uuid"
)

// maxDistinctBuilds is the largest number of distinct BuildIds a single
// Mappings can intern (cap of the uint16 dictionary index). Real headers
// reference a handful of builds; tripping this is a bug, not a deploy.
const maxDistinctBuilds = math.MaxUint16 + 1

// Mappings is the in-memory representation of a Header's mapping list.
//
// On the wire each mapping is a 40-byte BuildMap (uuid.UUID is 16 bytes
// inline). In memory we store the fields in parallel slices and intern
// each distinct BuildId once in builds, indexed by a uint16 per entry.
// Net per-entry footprint drops from 40 to 26 bytes plus 16 bytes per
// distinct build, which is a ~35% reduction on the dominant cached-header
// retain on snapshot-heavy orchestrator nodes.
//
// The zero value is empty and safe to use. Construction goes through
// MappingsFromSlice or MappingsBuilder; the SoA layout is otherwise an
// implementation detail and callers see only BuildMap values.
type Mappings struct {
	offsets        []uint64
	lengths        []uint64
	storageOffsets []uint64
	buildIdxs      []uint16
	builds         []uuid.UUID
}

// MappingsFromSlice packs maps into a Mappings, interning distinct BuildIds.
// Panics if the input references more than maxDistinctBuilds distinct
// BuildIds — which would mean a header has grown well past anything we
// expect in practice and is almost certainly a bug.
func MappingsFromSlice(maps []BuildMap) Mappings {
	if len(maps) == 0 {
		return Mappings{}
	}

	b := newMappingsBuilder(len(maps))
	for i := range maps {
		b.append(maps[i])
	}
	return b.build()
}

// Len returns the number of mappings.
func (m Mappings) Len() int { return len(m.offsets) }

// At returns the BuildMap at index i. The returned value is materialized
// (the BuildId is looked up in the internal dictionary), so callers can
// keep using the BuildMap value type unchanged.
func (m Mappings) At(i int) BuildMap {
	return BuildMap{
		Offset:             m.offsets[i],
		Length:             m.lengths[i],
		BuildId:            m.builds[m.buildIdxs[i]],
		BuildStorageOffset: m.storageOffsets[i],
	}
}

// OffsetAt is used by the binary-search hot path (Header.getMapping) and
// avoids materializing the full BuildMap on every probe.
func (m Mappings) OffsetAt(i int) uint64 { return m.offsets[i] }

// All yields (index, BuildMap) for range loops.
func (m Mappings) All() iter.Seq2[int, BuildMap] {
	return func(yield func(int, BuildMap) bool) {
		for i := range m.offsets {
			if !yield(i, m.At(i)) {
				return
			}
		}
	}
}

// Builds returns the distinct BuildIds referenced by this Mappings, in
// insertion order. The returned slice is read-only and aliases internal
// storage; callers must not mutate it.
func (m Mappings) Builds() []uuid.UUID { return m.builds }

// Slice materializes the full flat []BuildMap. Use only at serialization
// boundaries and in tools that need the legacy shape; the hot path should
// stick to At / OffsetAt / All.
func (m Mappings) Slice() []BuildMap {
	if len(m.offsets) == 0 {
		return nil
	}

	out := make([]BuildMap, len(m.offsets))
	for i := range out {
		out[i] = m.At(i)
	}
	return out
}

type mappingsBuilder struct {
	m     Mappings
	idxOf map[uuid.UUID]uint16
}

func newMappingsBuilder(capHint int) *mappingsBuilder {
	return &mappingsBuilder{
		m: Mappings{
			offsets:        make([]uint64, 0, capHint),
			lengths:        make([]uint64, 0, capHint),
			storageOffsets: make([]uint64, 0, capHint),
			buildIdxs:      make([]uint16, 0, capHint),
		},
		idxOf: make(map[uuid.UUID]uint16),
	}
}

func (b *mappingsBuilder) append(bm BuildMap) {
	idx, ok := b.idxOf[bm.BuildId]
	if !ok {
		if len(b.m.builds) >= maxDistinctBuilds {
			panic(fmt.Sprintf("header: more than %d distinct BuildIds in a single Mappings", maxDistinctBuilds))
		}
		idx = uint16(len(b.m.builds))
		b.m.builds = append(b.m.builds, bm.BuildId)
		b.idxOf[bm.BuildId] = idx
	}
	b.m.offsets = append(b.m.offsets, bm.Offset)
	b.m.lengths = append(b.m.lengths, bm.Length)
	b.m.storageOffsets = append(b.m.storageOffsets, bm.BuildStorageOffset)
	b.m.buildIdxs = append(b.m.buildIdxs, idx)
}

func (b *mappingsBuilder) build() Mappings { return b.m }

// entrySize returns the in-memory bytes per entry held by this Mappings
// (parallel slices, ignoring slice-header overhead and the small builds
// dictionary). Exposed for tests and tooling.
func (m Mappings) entrySize() int { return 8 + 8 + 8 + 2 }

