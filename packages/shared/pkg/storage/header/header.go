package header

import (
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
)

type Header struct {
	Metadata    *Metadata
	blockStarts *bitset.BitSet
	startMap    map[int64]*BuildMap

	Mapping []*BuildMap
}

func NewHeader(metadata *Metadata, mapping []*BuildMap) (*Header, error) {
	if metadata.BlockSize == 0 {
		return nil, fmt.Errorf("block size cannot be zero")
	}

	if len(mapping) == 0 {
		mapping = []*BuildMap{{
			Offset:             0,
			Length:             metadata.Size,
			BuildId:            metadata.BuildId,
			BuildStorageOffset: 0,
		}}
	}

	blocks := TotalBlocks(int64(metadata.Size), int64(metadata.BlockSize))

	intervals := bitset.New(uint(blocks))
	startMap := make(map[int64]*BuildMap, len(mapping))

	for _, mapping := range mapping {
		block := BlockIdx(int64(mapping.Offset), int64(metadata.BlockSize))

		intervals.Set(uint(block))
		startMap[block] = mapping
	}

	return &Header{
		blockStarts: intervals,
		Metadata:    metadata,
		Mapping:     mapping,
		startMap:    startMap,
	}, nil
}

func (t *Header) GetShiftedMapping(offset int64) (mappedOffset int64, mappedLength int64, buildID *uuid.UUID, err error) {
	mapping, shift, err := t.getMapping(offset)
	if err != nil {
		return 0, 0, nil, err
	}

	mappedOffset = int64(mapping.BuildStorageOffset) + shift
	mappedLength = int64(mapping.Length) - shift
	buildID = &mapping.BuildId

	if mappedLength < 0 {
		return 0, 0, nil, fmt.Errorf("mapped length for offset %d is negative: %d", offset, mappedLength)
	}

	return mappedOffset, mappedLength, buildID, nil
}

func (t *Header) getMapping(offset int64) (*BuildMap, int64, error) {
	if offset < 0 || offset >= int64(t.Metadata.Size) {
		return nil, 0, fmt.Errorf("offset %d is out of bounds (size: %d)", offset, t.Metadata.Size)
	}
	if offset%int64(t.Metadata.BlockSize) != 0 {
		return nil, 0, fmt.Errorf("offset %d is not aligned to block size %d", offset, t.Metadata.BlockSize)
	}

	block := BlockIdx(offset, int64(t.Metadata.BlockSize))

	start, ok := t.blockStarts.PreviousSet(uint(block))
	if !ok {
		return nil, 0, fmt.Errorf("no source found for offset %d", offset)
	}

	mapping, ok := t.startMap[int64(start)]
	if !ok {
		return nil, 0, fmt.Errorf("no mapping found for offset %d", offset)
	}

	shift := (block - int64(start)) * int64(t.Metadata.BlockSize)

	// Verify that the offset falls within this mapping's range
	if shift >= int64(mapping.Length) {
		return nil, 0, fmt.Errorf("offset %d (block %d) is beyond the end of mapping at offset %d (ends at %d)",
			offset, block, mapping.Offset, mapping.Offset+mapping.Length)
	}

	return mapping, shift, nil
}
