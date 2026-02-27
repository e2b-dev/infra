package header

import (
	"context"
	"fmt"

	"github.com/bits-and-blooms/bitset"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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

	h := &Header{
		blockStarts: intervals,
		Metadata:    metadata,
		Mapping:     mapping,
		startMap:    startMap,
	}

	// Validate header integrity at creation time
	if err := ValidateHeader(h); err != nil {
		return nil, fmt.Errorf("header validation failed: %w", err)
	}

	return h, nil
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
		Length:     uint64(lengthInBuild),
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

// ValidateHeader checks header integrity and returns an error if corruption is detected.
// This verifies:
// 1. Header and metadata are valid
// 2. Mappings cover the entire file [0, Size) with no gaps
// 3. Mappings don't extend beyond file size (with block alignment tolerance)
func ValidateHeader(h *Header) error {
	if h == nil {
		return fmt.Errorf("header is nil")
	}
	if h.Metadata == nil {
		return fmt.Errorf("header metadata is nil")
	}
	if h.Metadata.BlockSize == 0 {
		return fmt.Errorf("header has zero block size")
	}
	if h.Metadata.Size == 0 {
		return fmt.Errorf("header has zero size")
	}
	if len(h.Mapping) == 0 {
		return fmt.Errorf("header has no mappings")
	}

	// Sort mappings by offset to check for gaps/overlaps
	sortedMappings := make([]*BuildMap, len(h.Mapping))
	copy(sortedMappings, h.Mapping)
	for i := range len(sortedMappings) - 1 {
		for j := i + 1; j < len(sortedMappings); j++ {
			if sortedMappings[j].Offset < sortedMappings[i].Offset {
				sortedMappings[i], sortedMappings[j] = sortedMappings[j], sortedMappings[i]
			}
		}
	}

	// Check that first mapping starts at 0
	if sortedMappings[0].Offset != 0 {
		return fmt.Errorf("mappings don't start at 0: first mapping starts at %#x for buildId %s",
			sortedMappings[0].Offset, h.Metadata.BuildId.String())
	}

	// Check for gaps and overlaps between consecutive mappings
	for i := range len(sortedMappings) - 1 {
		currentEnd := sortedMappings[i].Offset + sortedMappings[i].Length
		nextStart := sortedMappings[i+1].Offset

		if currentEnd < nextStart {
			return fmt.Errorf("gap in mappings: mapping[%d] ends at %#x but mapping[%d] starts at %#x (gap=%d bytes) for buildId %s",
				i, currentEnd, i+1, nextStart, nextStart-currentEnd, h.Metadata.BuildId.String())
		}
		if currentEnd > nextStart {
			return fmt.Errorf("overlap in mappings: mapping[%d] ends at %#x but mapping[%d] starts at %#x (overlap=%d bytes) for buildId %s",
				i, currentEnd, i+1, nextStart, currentEnd-nextStart, h.Metadata.BuildId.String())
		}
	}

	// Check that last mapping covers up to (at least) Size
	lastMapping := sortedMappings[len(sortedMappings)-1]
	lastEnd := lastMapping.Offset + lastMapping.Length
	if lastEnd < h.Metadata.Size {
		return fmt.Errorf("mappings don't cover entire file: last mapping ends at %#x but file size is %#x (missing %d bytes) for buildId %s",
			lastEnd, h.Metadata.Size, h.Metadata.Size-lastEnd, h.Metadata.BuildId.String())
	}

	// Allow last mapping to extend up to one block past size (for alignment)
	if lastEnd > h.Metadata.Size+h.Metadata.BlockSize {
		return fmt.Errorf("last mapping extends too far: ends at %#x but file size is %#x (overhang=%d bytes, max allowed=%d) for buildId %s",
			lastEnd, h.Metadata.Size, lastEnd-h.Metadata.Size, h.Metadata.BlockSize, h.Metadata.BuildId.String())
	}

	// Validate individual mapping bounds
	for i, m := range h.Mapping {
		if m.Offset > h.Metadata.Size {
			return fmt.Errorf("mapping[%d] has Offset %#x beyond header size %#x for buildId %s",
				i, m.Offset, h.Metadata.Size, m.BuildId.String())
		}
		if m.Length == 0 {
			return fmt.Errorf("mapping[%d] has zero length at offset %#x for buildId %s",
				i, m.Offset, m.BuildId.String())
		}
	}

	return nil
}

// AddFrames associates compression frame information with this header's mappings.
//
// Only mappings matching this header's BuildId will be updated. Returns nil if frameTable is nil.
func (t *Header) AddFrames(frameTable *storage.FrameTable) error {
	if frameTable == nil {
		return nil
	}

	for _, mapping := range t.Mapping {
		if mapping.BuildId == t.Metadata.BuildId {
			if err := mapping.AddFrames(frameTable); err != nil {
				return err
			}
		}
	}

	return nil
}
