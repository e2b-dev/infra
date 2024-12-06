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

func CreateMapping(
	metadata *metadata,
	buildId *uuid.UUID,
	dirty *bitset.BitSet,
) ([]*buildMap, error) {
	var mappings []*buildMap

	var startBlock uint
	var blockLength uint
	var buildStorageOffset uint64

	for blockIdx, e := dirty.NextSet(0); e; blockIdx, e = dirty.NextSet(blockIdx + 1) {
		if startBlock+blockLength == blockIdx {
			blockLength++

			continue
		}

		if blockLength > 0 {
			m := &buildMap{
				Offset:             uint64(int64(startBlock) * metadata.BlockSize),
				BuildId:            *buildId,
				Length:             uint64(blockLength) * uint64(metadata.BlockSize),
				BuildStorageOffset: buildStorageOffset,
			}

			mappings = append(mappings, m)

			buildStorageOffset += m.Length
		}

		startBlock = blockIdx
		blockLength = 1
	}

	mappings = append(mappings, &buildMap{
		Offset:             uint64(int64(startBlock) * metadata.BlockSize),
		BuildId:            *buildId,
		Length:             uint64(blockLength) * uint64(metadata.BlockSize),
		BuildStorageOffset: buildStorageOffset,
	})

	return mappings, nil
}

func MergeMappings(baseMappings []*buildMap, newMappings []*buildMap) []*buildMap {
}
