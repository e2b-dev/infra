package header

import (
	"context"
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const NormalizeFixVersion = 3

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

func (t *Header) String() string {
	if t == nil {
		return "[nil Header]"
	}
	return fmt.Sprintf("[Header: version=%d, size=%d, blockSize=%d, generation=%d, buildId=%s, mappings=%d]",
		t.Metadata.Version,
		t.Metadata.Size,
		t.Metadata.BlockSize,
		t.Metadata.Generation,
		t.Metadata.BuildId.String(),
		len(t.Mapping),
	)
}

func (t *Header) Mappings(all bool) string {
	if t == nil {
		return "[nil Header, no mappings]"
	}
	n := 0
	for _, m := range t.Mapping {
		if all || m.BuildId == t.Metadata.BuildId {
			n++
		}
	}
	result := fmt.Sprintf("All mappings: %d\n", n)
	if !all {
		result = fmt.Sprintf("Mappings for build %s: %d\n", t.Metadata.BuildId.String(), n)
	}
	for _, m := range t.Mapping {
		if !all && m.BuildId != t.Metadata.BuildId {
			continue
		}
		frames := 0
		if m.FrameTable != nil {
			frames = len(m.FrameTable.Frames)
		}
		result += fmt.Sprintf("  - Offset: %#x, Length: %#x, BuildId: %s, BuildStorageOffset: %#x, numFrames: %d\n",
			m.Offset,
			m.Length,
			m.BuildId.String(),
			m.BuildStorageOffset,
			frames,
		)
	}
	return result
}

// IsNormalizeFixApplied is a helper method to soft fail for older versions of the header where fix for normalization was not applied.
// This should be removed in the future.
func (t *Header) IsNormalizeFixApplied() bool {
	return t.Metadata.Version >= NormalizeFixVersion
}

func (t *Header) GetShiftedMapping(ctx context.Context, offset int64) (mappedToBuild *BuildMap, err error) {
	mapping, shift, err := t.getMapping(ctx, offset)
	if err != nil {
		return nil, err
	}
	lengthInBuild := int64(mapping.Length) - shift

	b := &BuildMap{
		Offset:     mapping.BuildStorageOffset + uint64(shift),
		Length:     uint64(lengthInBuild), // TODO can we use int64 more consistently, or do we need the full uint64 range, which we kind of don't get anyway?
		BuildId:    mapping.BuildId,
		FrameTable: mapping.FrameTable,
	}

	if lengthInBuild < 0 {
		if t.IsNormalizeFixApplied() {
			return nil, fmt.Errorf("mapped length for offset %d is negative: %d", offset, lengthInBuild)
		}

		b.Length = 0
		logger.L().Warn(ctx, "mapped length is negative, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("mappedLength", lengthInBuild),
			logger.WithBuildID(mapping.BuildId.String()),
		)
	}

	return b, nil
}

// TODO: Maybe we can optimize mapping by automatically assuming the mapping is uuid.Nil if we don't find it + stopping storing the nil mapping.
func (t *Header) getMapping(ctx context.Context, offset int64) (*BuildMap, int64, error) {
	if offset < 0 || offset >= int64(t.Metadata.Size) {
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d is out of bounds (size: %d)", offset, t.Metadata.Size)
		}

		logger.L().Warn(ctx, "offset is out of bounds, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("size", int64(t.Metadata.Size)),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
	}
	if offset%int64(t.Metadata.BlockSize) != 0 {
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d is not aligned to block size %d", offset, t.Metadata.BlockSize)
		}

		logger.L().Warn(ctx, "offset is not aligned to block size, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("blockSize", int64(t.Metadata.BlockSize)),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
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
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d (block %d) is beyond the end of mapping at offset %d (ends at %d)",
				offset, block, mapping.Offset, mapping.Offset+mapping.Length)
		}

		logger.L().Warn(ctx, "offset is beyond the end of mapping, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("block", block),
			zap.Uint64("mappingOffset", mapping.Offset),
			zap.Uint64("mappingEnd", mapping.Offset+mapping.Length),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
	}

	return mapping, shift, nil
}
