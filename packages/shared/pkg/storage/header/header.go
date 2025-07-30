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

func NewHeader(metadata *Metadata, mapping []*BuildMap) *Header {
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
	}
}

func (t *Header) GetShiftedMapping(offset int64) (mappedOffset int64, mappedLength int64, buildID *uuid.UUID, err error) {
	mapping, shift, err := t.getMapping(offset)
	if err != nil {
		return 0, 0, nil, err
	}

	return int64(mapping.BuildStorageOffset) + shift, int64(mapping.BuildStorageSize) - shift, &mapping.BuildId, nil
}

func (t *Header) getMapping(offset int64) (*BuildMap, int64, error) {
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

	return mapping, shift, nil
}
