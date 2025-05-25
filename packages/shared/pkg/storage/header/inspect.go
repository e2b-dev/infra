package header

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

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
			return fmt.Errorf("mapping validation failed: the following mapping\n- %s\ndoes not start at the correct offset: expected %d (block %d), got %d (block %d)", mapping.Format(blockSize), currentOffset, currentOffset/blockSize, mapping.Offset, mapping.Offset/blockSize)
		}

		if mapping.Length%blockSize != 0 {
			return fmt.Errorf("mapping validation failed: the following mapping\n- %s\nhas an invalid length: %d. It should be a multiple of block size: %d", mapping.Format(blockSize), mapping.Length, blockSize)
		}

		if currentOffset+mapping.Length > size {
			return fmt.Errorf("mapping validation failed: the following mapping\n- %s\ngoes beyond the size: %d (current offset) + %d (length) > %d (size)", mapping.Format(blockSize), currentOffset, mapping.Length, size)
		}

		currentOffset += mapping.Length
	}

	if currentOffset != size {
		return fmt.Errorf("mapping validation failed: the following mapping\n- %s\ndoes not cover the whole size: %d (current offset) != %d (size)", mappings[len(mappings)-1].Format(blockSize), currentOffset, size)
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
