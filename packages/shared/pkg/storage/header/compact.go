package header

import (
	"errors"
	"fmt"
	"iter"

	"github.com/google/uuid"
)

// Mapping is the compact in-memory representation of a Header's mapping list.
// A merged Header is cached for up to 25h, so on snapshot-heavy nodes these
// slices dominate host RAM. It shrinks each entry from 40 bytes (a BuildMap) to
// 14 by encoding offset/length/storage as uint32 block indices, deduplicating
// BuildId into a per-header table addressed by uint16, and storing the columns
// as parallel slices. Immutable; read via At / All / Slice.
type Mapping struct {
	blockSize uint64
	builds    []uuid.UUID
	offsets   []uint32
	lengths   []uint32
	storage   []uint32
	buildIdx  []uint16
}

// maxBuildsPerHeader caps the per-header build-ID table at the uint16 limit.
const maxBuildsPerHeader = 1 << 16

// NewMapping packs src into the compact representation. blockSize is the unit
// for the block indices and must divide every Offset, Length, and
// BuildStorageOffset in src (callers pass PageSize, the universal granularity).
func NewMapping(blockSize uint64, src []BuildMap) (Mapping, error) {
	if blockSize == 0 {
		return Mapping{}, errors.New("compact mapping: block size cannot be zero")
	}
	if len(src) == 0 {
		return Mapping{blockSize: blockSize}, nil
	}

	// uint32 page-block index caps a single file at ~16 TiB (PageSize units).
	const maxBlockIdx = (1 << 32) - 1

	idxByBuild := make(map[uuid.UUID]uint16, 8)
	builds := make([]uuid.UUID, 0, 8)
	offsets := make([]uint32, len(src))
	lengths := make([]uint32, len(src))
	storage := make([]uint32, len(src))
	buildIdx := make([]uint16, len(src))

	for i, m := range src {
		if m.Offset%blockSize != 0 {
			return Mapping{}, fmt.Errorf("compact mapping: offset %d at index %d not block-aligned to %d", m.Offset, i, blockSize)
		}
		if m.Length%blockSize != 0 {
			return Mapping{}, fmt.Errorf("compact mapping: length %d at index %d not block-aligned to %d", m.Length, i, blockSize)
		}
		if m.BuildStorageOffset%blockSize != 0 {
			return Mapping{}, fmt.Errorf("compact mapping: build storage offset %d at index %d not block-aligned to %d", m.BuildStorageOffset, i, blockSize)
		}

		offBlocks := m.Offset / blockSize
		lenBlocks := m.Length / blockSize
		stoBlocks := m.BuildStorageOffset / blockSize
		if offBlocks > maxBlockIdx || lenBlocks > maxBlockIdx || stoBlocks > maxBlockIdx {
			return Mapping{}, fmt.Errorf("compact mapping: block index out of uint32 range at entry %d", i)
		}

		idx, ok := idxByBuild[m.BuildId]
		if !ok {
			if len(builds) >= maxBuildsPerHeader {
				return Mapping{}, fmt.Errorf("compact mapping: more than %d unique build IDs", maxBuildsPerHeader)
			}
			idx = uint16(len(builds))
			idxByBuild[m.BuildId] = idx
			builds = append(builds, m.BuildId)
		}

		offsets[i] = uint32(offBlocks)
		lengths[i] = uint32(lenBlocks)
		storage[i] = uint32(stoBlocks)
		buildIdx[i] = idx
	}

	return Mapping{
		blockSize: blockSize,
		builds:    builds,
		offsets:   offsets,
		lengths:   lengths,
		storage:   storage,
		buildIdx:  buildIdx,
	}, nil
}

// Len returns the number of entries.
func (m Mapping) Len() int { return len(m.offsets) }

// BlockSize returns the block size used for block<->byte conversions.
func (m Mapping) BlockSize() uint64 { return m.blockSize }

// Builds returns the deduplicated build IDs referenced by the mapping. The
// returned slice is shared with the Mapping; callers must not mutate it.
func (m Mapping) Builds() []uuid.UUID { return m.builds }

// At materializes the i-th entry as a BuildMap. Panics if i is out of range,
// matching `mapping[i]` semantics.
func (m Mapping) At(i int) BuildMap {
	return BuildMap{
		Offset:             uint64(m.offsets[i]) * m.blockSize,
		Length:             uint64(m.lengths[i]) * m.blockSize,
		BuildId:            m.builds[m.buildIdx[i]],
		BuildStorageOffset: uint64(m.storage[i]) * m.blockSize,
	}
}

// All iterates the mapping, materializing each entry as a BuildMap. This is
// the preferred read path for callers that don't need a backing []BuildMap.
func (m Mapping) All() iter.Seq2[int, BuildMap] {
	return func(yield func(int, BuildMap) bool) {
		for i := range m.offsets {
			if !yield(i, m.At(i)) {
				return
			}
		}
	}
}

// Slice materializes the full mapping as []BuildMap (~40 bytes/entry). Use
// sparingly — for serialization fallbacks, CLI inspection, and tests. Hot
// paths and the cached form should use At / All instead.
func (m Mapping) Slice() []BuildMap {
	out := make([]BuildMap, len(m.offsets))
	for i := range m.offsets {
		out[i] = m.At(i)
	}

	return out
}

// Validate checks that entries are contiguous, block-aligned to blockSize, and
// cover exactly `size` bytes. It is the compact-form equivalent of
// ValidateMappings(m.Slice(), size, blockSize) without the materialization.
func (m Mapping) Validate(size, blockSize uint64) error {
	if blockSize == 0 {
		return errors.New("mapping validation failed: zero block size")
	}
	// The compact form stores in m.blockSize units; if those aren't a multiple
	// of the requested validation blockSize, fall back to the slice path so we
	// don't miss a misalignment.
	if m.blockSize%blockSize != 0 {
		return ValidateMappings(m.Slice(), size, blockSize)
	}

	var currentOffset uint64
	for i := range m.offsets {
		offset := uint64(m.offsets[i]) * m.blockSize
		length := uint64(m.lengths[i]) * m.blockSize
		if currentOffset != offset {
			return fmt.Errorf("mapping validation failed at index %d: expected offset %d (block %d), got %d (block %d)", i, currentOffset, currentOffset/blockSize, offset, offset/blockSize)
		}
		if currentOffset+length > size {
			return fmt.Errorf("mapping validation failed at index %d: %d (current offset) + %d (length) > %d (size)", i, currentOffset, length, size)
		}
		currentOffset += length
	}
	if currentOffset != size {
		return fmt.Errorf("mapping validation failed: total %d != size %d", currentOffset, size)
	}

	return nil
}

// SearchOffset returns the first index i whose byte offset is strictly greater
// than off, matching sort.Search semantics on Offset. It compares in block
// units, so entries are never materialized: entry.OffsetBlocks*blockSize > off
// iff entry.OffsetBlocks > off/blockSize (integer division).
func (m Mapping) SearchOffset(off int64) int {
	if off < 0 || len(m.offsets) == 0 {
		return 0
	}
	target := uint64(off) / m.blockSize
	lo, hi := 0, len(m.offsets)
	for lo < hi {
		mid := int(uint(lo+hi) >> 1)
		if uint64(m.offsets[mid]) > target {
			hi = mid
		} else {
			lo = mid + 1
		}
	}

	return lo
}
