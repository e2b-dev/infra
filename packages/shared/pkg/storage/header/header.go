package header

import (
	"cmp"
	"context"
	"fmt"
	"maps"
	"slices"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// BuildData holds per-build metadata stored in V4 headers.
type BuildData struct {
	Size      int64               // uncompressed file size
	Checksum  [32]byte            // SHA-256 of uncompressed data; zero value means unknown
	FrameData *storage.FrameTable // nil for uncompressed builds
}

const NormalizeFixVersion = 3

type Header struct {
	Metadata *Metadata
	// Builds maps build IDs to per-build metadata. V3 headers have nil Builds;
	// the read path falls back to a Size() RPC for those.
	Builds      map[uuid.UUID]BuildData
	blockStarts *bitset.BitSet
	startMap    map[int64]*BuildMap

	Mapping []*BuildMap
}

// CloneForUpload returns a shallow clone safe for serialization without
// racing with concurrent readers.
func (t *Header) CloneForUpload() *Header {
	mappings := make([]*BuildMap, len(t.Mapping))
	copy(mappings, t.Mapping)

	metaCopy := *t.Metadata
	clone := &Header{
		Metadata: &metaCopy,
		Mapping:  mappings,
	}

	if t.Builds != nil {
		clone.Builds = make(map[uuid.UUID]BuildData, len(t.Builds))
		maps.Copy(clone.Builds, t.Builds)
	}

	return clone
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

	for _, m := range mapping {
		block := BlockIdx(int64(m.Offset), int64(metadata.BlockSize))

		intervals.Set(uint(block))
		startMap[block] = m
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

// IsNormalizeFixApplied is a helper method to soft fail for older versions of the header where fix for normalization was not applied.
// This should be removed in the future.
func (t *Header) IsNormalizeFixApplied() bool {
	return t.Metadata.Version >= NormalizeFixVersion
}

func (t *Header) GetShiftedMapping(ctx context.Context, offset int64) (BuildMap, error) {
	mapping, shift, err := t.getMapping(ctx, offset)
	if err != nil {
		return BuildMap{}, err
	}
	mappedLength := int64(mapping.Length) - shift

	b := BuildMap{
		Offset:  mapping.BuildStorageOffset + uint64(shift),
		Length:  uint64(mappedLength),
		BuildId: mapping.BuildId,
	}

	if mappedLength < 0 {
		if t.IsNormalizeFixApplied() {
			return BuildMap{}, fmt.Errorf("mapped length for offset %d is negative: %d", offset, mappedLength)
		}

		b.Length = 0
		logger.L().Warn(ctx, "mapped length is negative, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("mappedLength", mappedLength),
			logger.WithBuildID(mapping.BuildId.String()),
		)
	}

	return b, nil
}

// GetBuildFrameData returns the frame table for a build, or nil.
func (t *Header) GetBuildFrameData(buildID uuid.UUID) *storage.FrameTable {
	if t.Builds == nil {
		return nil
	}

	return t.Builds[buildID].FrameData
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
	slices.SortFunc(sortedMappings, func(a, b *BuildMap) int {
		return cmp.Compare(a.Offset, b.Offset)
	})

	// Check that first mapping starts at 0
	if sortedMappings[0].Offset != 0 {
		return fmt.Errorf("mappings don't start at 0: first mapping starts at %d for buildId %s",
			sortedMappings[0].Offset, h.Metadata.BuildId.String())
	}

	// Check for gaps and overlaps between consecutive mappings
	for i := range len(sortedMappings) - 1 {
		currentEnd := sortedMappings[i].Offset + sortedMappings[i].Length
		nextStart := sortedMappings[i+1].Offset

		if currentEnd < nextStart {
			return fmt.Errorf("gap in mappings: mapping[%d] ends at %d but mapping[%d] starts at %d (gap=%d bytes) for buildId %s",
				i, currentEnd, i+1, nextStart, nextStart-currentEnd, h.Metadata.BuildId.String())
		}
		if currentEnd > nextStart {
			return fmt.Errorf("overlap in mappings: mapping[%d] ends at %d but mapping[%d] starts at %d (overlap=%d bytes) for buildId %s",
				i, currentEnd, i+1, nextStart, currentEnd-nextStart, h.Metadata.BuildId.String())
		}
	}

	// Check that last mapping covers up to (at least) Size
	lastMapping := sortedMappings[len(sortedMappings)-1]
	lastEnd := lastMapping.Offset + lastMapping.Length
	if lastEnd < h.Metadata.Size {
		return fmt.Errorf("mappings don't cover entire file: last mapping ends at %d but file size is %d (missing %d bytes) for buildId %s",
			lastEnd, h.Metadata.Size, h.Metadata.Size-lastEnd, h.Metadata.BuildId.String())
	}

	// Allow last mapping to extend up to one block past size (for alignment)
	if lastEnd > h.Metadata.Size+h.Metadata.BlockSize {
		return fmt.Errorf("last mapping extends too far: ends at %d but file size is %d (overhang=%d bytes, max allowed=%d) for buildId %s",
			lastEnd, h.Metadata.Size, lastEnd-h.Metadata.Size, h.Metadata.BlockSize, h.Metadata.BuildId.String())
	}

	// Validate individual mapping bounds
	for i, m := range h.Mapping {
		if m.Offset > h.Metadata.Size {
			return fmt.Errorf("mapping[%d] has Offset %d beyond header size %d for buildId %s",
				i, m.Offset, h.Metadata.Size, m.BuildId.String())
		}
		if m.Length == 0 {
			return fmt.Errorf("mapping[%d] has zero length at offset %d for buildId %s",
				i, m.Offset, m.BuildId.String())
		}
	}

	return nil
}
