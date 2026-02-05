package header

import (
	"fmt"
	"os"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// Start, Length and SourceStart are in bytes of the data file
// Length will be a multiple of BlockSize
// The list of block mappings will be in order of increasing Start, covering the entire file
type BuildMap struct {
	// Offset defines which block of the current layer this mapping starts at
	Offset             uint64 // in the memory space
	Length             uint64
	BuildId            uuid.UUID
	BuildStorageOffset uint64
	FrameTable         *storage.FrameTable
}

func (mapping *BuildMap) Copy() *BuildMap {
	return &BuildMap{
		Offset:             mapping.Offset,
		Length:             mapping.Length,
		BuildId:            mapping.BuildId,
		BuildStorageOffset: mapping.BuildStorageOffset,
		FrameTable:         mapping.FrameTable, // Preserve FrameTable for compressed data
	}
}

// AddFrames associates compression frame information with this mapping.
//
// When a file is uploaded with compression, the compressor produces a FrameTable
// that describes how the compressed data is organized into frames. This method
// computes which compressed frames cover this mapping's data within the build's
// storage file based on BuildStorageOffset and Length.
//
// Returns nil if frameTable is nil. Returns an error if the mapping's range
// cannot be found in the frame table.
func (mapping *BuildMap) AddFrames(frameTable *storage.FrameTable) error {
	if frameTable == nil {
		return nil
	}

	mappedRange := storage.Range{
		Start:  int64(mapping.BuildStorageOffset),
		Length: int(mapping.Length),
	}

	subset, err := frameTable.Subset(mappedRange)
	if err != nil {
		return fmt.Errorf("mapping at virtual offset %#x (storage offset %#x, length %#x): %w",
			mapping.Offset, mapping.BuildStorageOffset, mapping.Length, err)
	}

	mapping.FrameTable = subset

	return nil
}

func CreateMapping(
	buildId *uuid.UUID,
	dirty *bitset.BitSet,
	blockSize int64,
) []*BuildMap {
	var mappings []*BuildMap

	var startBlock uint
	var blockLength uint
	var buildStorageOffset uint64

	for blockIdx, e := dirty.NextSet(0); e; blockIdx, e = dirty.NextSet(blockIdx + 1) {
		if startBlock+blockLength == blockIdx {
			blockLength++

			continue
		}

		if blockLength > 0 {
			m := &BuildMap{
				Offset:             uint64(startBlock) * uint64(blockSize),
				BuildId:            *buildId,
				Length:             uint64(blockLength) * uint64(blockSize),
				BuildStorageOffset: buildStorageOffset,
			}

			mappings = append(mappings, m)

			buildStorageOffset += m.Length
		}

		startBlock = blockIdx
		blockLength = 1
	}

	if blockLength > 0 {
		mappings = append(mappings, &BuildMap{
			Offset:             uint64(startBlock) * uint64(blockSize),
			BuildId:            *buildId,
			Length:             uint64(blockLength) * uint64(blockSize),
			BuildStorageOffset: buildStorageOffset,
		})
	}

	return mappings
}

// MergeMappings merges two sets of mappings.
//
// The mapping are stored in a sorted order.
// The baseMapping must cover the whole size.
//
// It returns a new set of mappings that covers the whole size.
func MergeMappings(
	baseMapping []*BuildMap,
	diffMapping []*BuildMap,
) []*BuildMap {
	if len(diffMapping) == 0 {
		return baseMapping
	}

	baseMappingCopy := make([]*BuildMap, len(baseMapping))

	copy(baseMappingCopy, baseMapping)

	baseMapping = baseMappingCopy

	mappings := make([]*BuildMap, 0)

	var baseIdx int
	var diffIdx int

	for baseIdx < len(baseMapping) && diffIdx < len(diffMapping) {
		base := baseMapping[baseIdx]
		diff := diffMapping[diffIdx]

		if base.Length == 0 {
			baseIdx++

			continue
		}

		if diff.Length == 0 {
			diffIdx++

			continue
		}

		// base is before diff and there is no overlap
		// add base to the result, because it will not be overlapping by any diff
		if base.Offset+base.Length <= diff.Offset {
			mappings = append(mappings, base)

			baseIdx++

			continue
		}

		// diff is before base and there is no overlap
		// add diff to the result, because we don't need to check if it overlaps with any base
		if diff.Offset+diff.Length <= base.Offset {
			mappings = append(mappings, diff)

			diffIdx++

			continue
		}

		// base is inside diff
		// remove base, because it's fully covered by diff
		if base.Offset >= diff.Offset && base.Offset+base.Length <= diff.Offset+diff.Length {
			baseIdx++

			continue
		}

		// diff is inside base (they start and end can also be the same)
		// split base into two parts: left part (before diff) and right part (after diff)
		// if left part is not empty, add it to the result
		// add diff to the result
		// if right part is not empty, update baseMapping with it, otherwise remove it from the baseMapping
		if diff.Offset >= base.Offset && diff.Offset+diff.Length <= base.Offset+base.Length {
			leftBaseLength := int64(diff.Offset) - int64(base.Offset)

			if leftBaseLength > 0 {
				leftBase := &BuildMap{
					Offset:  base.Offset,
					Length:  uint64(leftBaseLength),
					BuildId: base.BuildId,
					// the build storage offset is the same as the base mapping
					BuildStorageOffset: base.BuildStorageOffset,
				}
				leftBase.FrameTable, _ = base.FrameTable.Subset(storage.Range{Start: int64(leftBase.BuildStorageOffset), Length: int(leftBase.Length)})

				mappings = append(mappings, leftBase)
			}

			mappings = append(mappings, diff)

			diffIdx++

			rightBaseShift := int64(diff.Offset) + int64(diff.Length) - int64(base.Offset)
			rightBaseLength := int64(base.Length) - rightBaseShift

			if rightBaseLength > 0 {
				rightBase := &BuildMap{
					Offset:             base.Offset + uint64(rightBaseShift),
					Length:             uint64(rightBaseLength),
					BuildId:            base.BuildId,
					BuildStorageOffset: base.BuildStorageOffset + uint64(rightBaseShift),
				}
				rightBase.FrameTable, _ = base.FrameTable.Subset(storage.Range{Start: int64(rightBase.BuildStorageOffset), Length: int(rightBase.Length)})

				baseMapping[baseIdx] = rightBase
			} else {
				baseIdx++
			}

			continue
		}

		// base is after diff and there is overlap
		// add diff to the result
		// add the right part of base to the baseMapping, it should not be empty because of the check above
		if base.Offset > diff.Offset {
			mappings = append(mappings, diff)

			diffIdx++

			rightBaseShift := int64(diff.Offset) + int64(diff.Length) - int64(base.Offset)
			rightBaseLength := int64(base.Length) - rightBaseShift

			if rightBaseLength > 0 {
				rightBase := &BuildMap{
					Offset:             base.Offset + uint64(rightBaseShift),
					Length:             uint64(rightBaseLength),
					BuildId:            base.BuildId,
					BuildStorageOffset: base.BuildStorageOffset + uint64(rightBaseShift),
				}
				rightBase.FrameTable, _ = base.FrameTable.Subset(storage.Range{Start: int64(rightBase.BuildStorageOffset), Length: int(rightBase.Length)})

				baseMapping[baseIdx] = rightBase
			} else {
				baseIdx++
			}

			continue
		}

		// diff is after base and there is overlap
		// add the left part of base to the result, it should not be empty because of the check above
		if diff.Offset > base.Offset {
			leftBaseLength := int64(diff.Offset) - int64(base.Offset)

			if leftBaseLength > 0 {
				leftBase := &BuildMap{
					Offset:             base.Offset,
					Length:             uint64(leftBaseLength),
					BuildId:            base.BuildId,
					BuildStorageOffset: base.BuildStorageOffset,
				}
				leftBase.FrameTable, _ = base.FrameTable.Subset(storage.Range{Start: int64(leftBase.BuildStorageOffset), Length: int(leftBase.Length)})

				mappings = append(mappings, leftBase)
			}

			baseIdx++

			continue
		}

		fmt.Fprintf(os.Stderr, "invalid case during merge mappings: %+v %+v\n", base, diff)
	}

	mappings = append(mappings, baseMapping[baseIdx:]...)
	mappings = append(mappings, diffMapping[diffIdx:]...)

	return mappings
}

// NormalizeMappings joins adjacent mappings that have the same buildId.
// When merging mappings, FrameTables are also merged by extending the first
// mapping's FrameTable with frames from subsequent mappings.
func NormalizeMappings(mappings []*BuildMap) []*BuildMap {
	if len(mappings) == 0 {
		return nil
	}

	result := make([]*BuildMap, 0, len(mappings))

	// Start with a copy of the first mapping (Copy() now includes FrameTable)
	current := mappings[0].Copy()

	for i := 1; i < len(mappings); i++ {
		mp := mappings[i]
		if mp.BuildId != current.BuildId {
			// BuildId changed, add the current map to results and start a new one
			result = append(result, current)
			current = mp.Copy() // New copy (includes FrameTable)
		} else {
			// Same BuildId, merge: add the length and extend FrameTable
			current.Length += mp.Length

			// Extend FrameTable if the mapping being merged has one
			if mp.FrameTable != nil {
				if current.FrameTable == nil {
					// Current has no FrameTable but merged one does - take it
					current.FrameTable = mp.FrameTable
				} else {
					// Both have FrameTables - extend current's with mp's frames
					// The frames are contiguous subsets, so we append non-overlapping frames
					current.FrameTable = mergeFrameTables(current.FrameTable, mp.FrameTable)
				}
			}
		}
	}

	// Add the last mapping
	result = append(result, current)

	return result
}

// mergeFrameTables extends ft1 with frames from ft2. The FrameTables are
// assumed to be contiguous subsets from the same original, so ft2's frames
// follow ft1's frames (with possible overlap at the boundary). this function
// returns either an reference to one of the input tables, unchanged, or a new
// FrameTable with frames from both tables.
func mergeFrameTables(ft1, ft2 *storage.FrameTable) *storage.FrameTable {
	if ft1 == nil {
		return ft2
	}
	if ft2 == nil {
		return ft1
	}

	// Calculate where ft1 ends (uncompressed offset)
	ft1EndU := ft1.StartAt.U
	for _, frame := range ft1.Frames {
		ft1EndU += int64(frame.U)
	}

	// Find where to start appending from ft2 (skip frames already covered by ft1)
	ft2CurrentU := ft2.StartAt.U
	startIdx := 0
	for i, frame := range ft2.Frames {
		frameEndU := ft2CurrentU + int64(frame.U)
		if frameEndU <= ft1EndU {
			// This frame is already covered by ft1
			ft2CurrentU = frameEndU
			startIdx = i + 1

			continue
		}
		if ft2CurrentU < ft1EndU {
			// This frame overlaps with ft1's last frame - it's the same frame, skip it
			ft2CurrentU = frameEndU
			startIdx = i + 1

			continue
		}
		// This frame is beyond ft1's coverage
		break
	}

	// Append remaining frames from ft2
	if startIdx < len(ft2.Frames) {
		// Create a new FrameTable with extended frames
		newFrames := make([]storage.FrameSize, len(ft1.Frames), len(ft1.Frames)+len(ft2.Frames)-startIdx)
		copy(newFrames, ft1.Frames)
		newFrames = append(newFrames, ft2.Frames[startIdx:]...)

		return &storage.FrameTable{
			CompressionType: ft1.CompressionType,
			StartAt:         ft1.StartAt,
			Frames:          newFrames,
		}
	}

	// All of ft2's frames were already covered by ft1
	return ft1
}
