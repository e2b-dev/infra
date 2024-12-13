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

	return int64(mapping.BuildStorageOffset) + shift, int64(mapping.Length) - shift, &mapping.BuildId, nil
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

func CreateMapping(
	metadata *Metadata,
	buildId *uuid.UUID,
	dirty *bitset.BitSet,
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
				Offset:             uint64(int64(startBlock) * int64(metadata.BlockSize)),
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

	mappings = append(mappings, &BuildMap{
		Offset:             uint64(startBlock) * metadata.BlockSize,
		BuildId:            *buildId,
		Length:             uint64(blockLength) * uint64(metadata.BlockSize),
		BuildStorageOffset: buildStorageOffset,
	})

	return mappings
}

// The mapping are stored in a sorted order.
// The baseMapping must cover the whole size.
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
			leftBase := &BuildMap{
				Offset:  base.Offset,
				Length:  diff.Offset - base.Offset,
				BuildId: base.BuildId,
				// the build storage offset is the same as the base mapping
				BuildStorageOffset: base.BuildStorageOffset,
			}

			if leftBase.Length > 0 {
				mappings = append(mappings, leftBase)
			}

			mappings = append(mappings, diff)

			diffIdx++

			rightBaseShift := diff.Offset + diff.Length - base.Offset

			rightBase := &BuildMap{
				Offset:             base.Offset + rightBaseShift,
				Length:             base.Length - rightBaseShift,
				BuildId:            base.BuildId,
				BuildStorageOffset: base.BuildStorageOffset + rightBaseShift,
			}

			if rightBase.Length > 0 {
				baseMapping[baseIdx] = rightBase
			} else {
				baseIdx++
			}

			continue
		}

		// base is after diff and there is overlap
		// add diff to the result
		// add the right part of base to the baseMapping, it should not be empty because of the check above
		if base.Offset+base.Length > diff.Offset {
			mappings = append(mappings, diff)

			diffIdx++

			rightBaseShift := diff.Offset + diff.Length - base.Offset

			baseMapping[baseIdx] = &BuildMap{
				Offset:             base.Offset + rightBaseShift,
				Length:             base.Length - rightBaseShift,
				BuildId:            base.BuildId,
				BuildStorageOffset: base.BuildStorageOffset + rightBaseShift,
			}

			continue
		}

		// diff is after base and there is overlap
		// add the left part of base to the result, it should not be empty because of the check above
		if diff.Offset+diff.Length > base.Offset {
			mappings = append(mappings, &BuildMap{
				Offset:             base.Offset,
				Length:             diff.Offset - base.Offset,
				BuildId:            base.BuildId,
				BuildStorageOffset: base.BuildStorageOffset,
			})

			baseIdx++

			continue
		}
	}

	mappings = append(mappings, baseMapping[baseIdx:]...)
	mappings = append(mappings, diffMapping[diffIdx:]...)

	return mappings
}