package header

import (
	"fmt"
	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
	"os"
)

// Start, Length and SourceStart are in bytes of the data file
// Length will be a multiple of BlockSize
// The list of block mappings will be in order of increasing Start, covering the entire file
type BuildMap struct {
	// Offset defines which block of the current layer this mapping starts at
	Offset             uint64
	Length             uint64
	BuildId            uuid.UUID
	BuildStorageOffset uint64
}

func CreateMapping(
	buildId *uuid.UUID,
	dirty *bitset.BitSet,
	blockSize uint64,
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
				Offset:             uint64(startBlock) * blockSize,
				BuildId:            *buildId,
				Length:             uint64(blockLength) * blockSize,
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
			Offset:             uint64(startBlock) * blockSize,
			BuildId:            *buildId,
			Length:             uint64(blockLength) * blockSize,
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
