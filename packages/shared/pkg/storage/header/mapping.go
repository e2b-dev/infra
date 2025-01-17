package header

import (
	"fmt"
	"os"
	"strings"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
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
				Offset:             uint64(int64(startBlock) * int64(blockSize)),
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

		if base.Length <= 0 {
			baseIdx++

			continue
		}

		if diff.Length <= 0 {
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

// Join adjanced mappings that have the same buildId.
func NormalizeMappings(mappings []*BuildMap) []*BuildMap {
	for i := 0; i < len(mappings); i++ {
		if i+1 < len(mappings) && mappings[i].BuildId == mappings[i+1].BuildId {
			mappings[i].Length += mappings[i+1].Length
			mappings = append(mappings[:i+1], mappings[i+2:]...)
		}
	}

	return mappings
}

// Format returns a string representation of the mapping as:
//
// startBlock-endBlock [offset, offset+length) := [buildStorageOffset, buildStorageOffset+length) ⊂ buildId, length in bytes
//
// It is used for debugging and visualization.
func (mapping *BuildMap) Format(blockSize uint64) string {
	rangeMessage := fmt.Sprintf("%d-%d", mapping.Offset/blockSize, (mapping.Offset+mapping.Length)/blockSize)

	return fmt.Sprintf(
		"%-14s [%11d,%11d) := [%11d,%11d) ⊂ %s, %d B",
		rangeMessage,
		mapping.Offset, mapping.Offset+mapping.Length,
		mapping.BuildStorageOffset, mapping.BuildStorageOffset+mapping.Length, mapping.BuildId.String(), mapping.Length,
	)
}

const (
	SkippedBlockChar = '░'
	DirtyBlockChar1  = '▓'
	DirtyBlockChar2  = '█'
)

// Layers returns a map of buildIds that are present in the mappings.
func Layers(mappings []*BuildMap) *map[uuid.UUID]struct{} {
	layers := make(map[uuid.UUID]struct{})

	for _, mapping := range mappings {
		layers[mapping.BuildId] = struct{}{}
	}

	return &layers
}

// Visualize returns a string representation of the mappings as a grid of blocks.
// It is used for debugging and visualization.
//
// You can pass maps to visualize different groups of buildIds.
func Visualize(mappings []*BuildMap, size, blockSize, cols uint64, bottomGroup, topGroup *map[uuid.UUID]struct{}) string {
	output := make([]rune, size/blockSize)

	for outputIdx := range output {
		output[outputIdx] = SkippedBlockChar
	}

	for _, mapping := range mappings {
		for block := uint64(0); block < mapping.Length/blockSize; block++ {
			if bottomGroup != nil {
				if _, ok := (*bottomGroup)[mapping.BuildId]; ok {
					output[mapping.Offset/blockSize+block] = DirtyBlockChar1
				}
			}

			if topGroup != nil {
				if _, ok := (*topGroup)[mapping.BuildId]; ok {
					output[mapping.Offset/blockSize+block] = DirtyBlockChar2
				}
			}
		}
	}

	lineOutput := make([]string, 0)

	for i := uint64(0); i < size/blockSize; i += cols {
		if i+cols <= uint64(len(output)) {
			lineOutput = append(lineOutput, string(output[i:i+cols]))
		} else {
			lineOutput = append(lineOutput, string(output[i:]))
		}
	}

	return strings.Join(lineOutput, "\n")
}

// ValidateMappings validates the mappings.
// It is used to check if the mappings are valid.
//
// It checks if the mappings are contiguous and if the length of each mapping is a multiple of the block size.
// It also checks if the mappings cover the whole size.
func ValidateMappings(mappings []*BuildMap, size, blockSize uint64) error {
	var currentOffset uint64

	for _, mapping := range mappings {
		if currentOffset != mapping.Offset {
			return fmt.Errorf("mapping validation failed: the following mapping\n- %s\ndoes not start at the correct offset: expected %d (block %d), got %d (block %d)\n", mapping.Format(blockSize), currentOffset, currentOffset/blockSize, mapping.Offset, mapping.Offset/blockSize)
		}

		if mapping.Length%blockSize != 0 {
			return fmt.Errorf("mapping validation failed: the following mapping\n- %s\nhas an invalid length: %d. It should be a multiple of block size: %d\n", mapping.Format(blockSize), mapping.Length, blockSize)
		}

		if currentOffset+mapping.Length > size {
			return fmt.Errorf("mapping validation failed: the following mapping\n- %s\ngoes beyond the size: %d (current offset) + %d (length) > %d (size)\n", mapping.Format(blockSize), currentOffset, mapping.Length, size)
		}

		currentOffset += mapping.Length
	}

	if currentOffset != size {
		return fmt.Errorf("mapping validation failed: the following mapping\n- %s\ndoes not cover the whole size: %d (current offset) != %d (size)\n", mappings[len(mappings)-1].Format(blockSize), currentOffset, size)
	}

	return nil
}

func (mapping *BuildMap) Equal(other *BuildMap) bool {
	return mapping.Offset == other.Offset && mapping.Length == other.Length && mapping.BuildId == other.BuildId
}

func Equal(a, b []*BuildMap) bool {
	if len(a) != len(b) {
		return false
	}

	for i := range a {
		if !a[i].Equal(b[i]) {
			return false
		}
	}

	return true
}
