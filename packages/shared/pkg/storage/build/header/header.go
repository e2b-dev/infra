package header

import (
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
)

type Header struct {
	Metadata    *metadata
	blockStarts *bitset.BitSet
	startMap    map[uint64]*buildMap

	Mapping []*buildMap
}

func NewHeader(metadata *metadata, mapping []*buildMap) *Header {
	blocks := NumberOfBlocks(metadata.Size, metadata.BlockSize)

	intervals := bitset.New(blocks)
	startMap := make(map[uint64]*buildMap, len(mapping))

	for _, mapping := range mapping {
		intervals.Set(uint(mapping.Offset))
		startMap[mapping.Offset] = mapping
	}

	return &Header{
		blockStarts: intervals,
		Metadata:    metadata,
		Mapping:     mapping,
		startMap:    startMap,
	}
}

func (t *Header) GetMapping(offset int64) (*buildMap, error) {
	block := offset / t.Metadata.BlockSize

	start, ok := t.blockStarts.PreviousSet(uint(block))
	if !ok {
		return nil, fmt.Errorf("no source found for offset %d", offset)
	}

	mapping, ok := t.startMap[uint64(start)]
	if !ok {
		return nil, fmt.Errorf("no mapping found for offset %d", offset)
	}

	return mapping, nil
}

func createMapping(
	metadata *metadata,
	buildId *uuid.UUID,
	change *bitset.BitSet,
) ([]*buildMap, error) {
	var mappings []*buildMap

	var currentMapping *buildMap

	var dataOffset uint64

	for i, e := change.NextSet(0); e; i, e = change.NextSet(i + 1) {
		if currentMapping == nil {
			currentMapping = &buildMap{
				Offset:  uint64(int64(i) * metadata.BlockSize),
				BuildId: *buildId,
			}
		}

		change.PreviousSet(i - 1)

		dataOffset += uint64(metadata.BlockSize)
	}

	return mappings, nil
}
