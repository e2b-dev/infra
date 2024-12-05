package header

import (
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
)

type Header struct {
	metadata    *metadata
	blockStarts *bitset.BitSet
	startMap    map[uint64]*blockMapping
	blockMap    []*blockMapping
}

func NumberOfBlocks(size, blockSize int64) uint {
	return uint((size + blockSize - 1) / blockSize)
}

func NewHeader(metadata *metadata, blockMap []*blockMapping) *Header {
	blocks := NumberOfBlocks(metadata.Size, metadata.BlockSize)

	intervals := bitset.New(blocks)
	startMap := make(map[uint64]*blockMapping, len(blockMap))

	for _, mapping := range blockMap {
		intervals.Set(uint(mapping.Start))
		startMap[mapping.Start] = mapping
	}

	return &Header{
		blockStarts: intervals,
		metadata:    metadata,
		blockMap:    blockMap,
		startMap:    startMap,
	}
}

func (t *Header) GetMapping(offset int64) (*blockMapping, error) {
	block := offset / t.metadata.BlockSize

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

func (t *Header) BlockMap() []*blockMapping {
	return t.blockMap
}

func (t *Header) Metadata() *metadata {
	return t.metadata
}

func createMapping(
	metadata *metadata,
	changeSource *uuid.UUID,
	change *bitset.BitSet,
) ([]*blockMapping, error) {
	var mappings []*blockMapping

	var currentMapping *blockMapping

	var dataOffset uint64

	for i, e := change.NextSet(0); e; i, e = change.NextSet(i + 1) {
		if currentMapping == nil {
			currentMapping = &blockMapping{
				Start:  uint64(int64(i) * metadata.BlockSize),
				Source: *changeSource,
			}
		}

		change.PreviousSet(i - 1)

		dataOffset += uint64(metadata.BlockSize)
	}

	return mappings, nil
}
