package header

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

const NormalizeFixVersion = 3

type Header struct {
	Metadata *Metadata
	Mapping  []BuildMap
}

func NewHeader(metadata *Metadata, mapping []BuildMap) (*Header, error) {
	if metadata.BlockSize == 0 {
		return nil, errors.New("block size cannot be zero")
	}

	if len(mapping) == 0 {
		mapping = []BuildMap{{
			Offset:             0,
			Length:             metadata.Size,
			BuildId:            metadata.BuildId,
			BuildStorageOffset: 0,
		}}
	}

	return &Header{
		Metadata: metadata,
		Mapping:  mapping,
	}, nil
}

// IsNormalizeFixApplied is a helper method to soft fail for older versions of the header where fix for normalization was not applied.
// This should be removed in the future.
func (t *Header) IsNormalizeFixApplied() bool {
	return t.Metadata.Version >= NormalizeFixVersion
}

func (t *Header) GetShiftedMapping(ctx context.Context, offset int64) (mappedOffset int64, mappedLength int64, buildID *uuid.UUID, err error) {
	mapping, shift, err := t.getMapping(ctx, offset)
	if err != nil {
		return 0, 0, nil, err
	}

	mappedOffset = int64(mapping.BuildStorageOffset) + shift
	mappedLength = int64(mapping.Length) - shift
	buildID = &mapping.BuildId

	if mappedLength < 0 {
		if t.IsNormalizeFixApplied() {
			return 0, 0, nil, fmt.Errorf("mapped length for offset %d is negative: %d", offset, mappedLength)
		}

		logger.L().Warn(ctx, "mapped length is negative, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Int64("mappedLength", mappedLength),
			logger.WithBuildID(mapping.BuildId.String()),
		)
	}

	return mappedOffset, mappedLength, buildID, nil
}

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

	i := sort.Search(len(t.Mapping), func(i int) bool {
		return int64(t.Mapping[i].Offset) > offset
	})

	if i == 0 {
		return nil, 0, fmt.Errorf("no source found for offset %d", offset)
	}

	mapping := &t.Mapping[i-1]
	shift := offset - int64(mapping.Offset)

	// Verify that the offset falls within this mapping's range
	if shift >= int64(mapping.Length) {
		if t.IsNormalizeFixApplied() {
			return nil, 0, fmt.Errorf("offset %d is beyond the end of mapping at offset %d (ends at %d)",
				offset, mapping.Offset, mapping.Offset+mapping.Length)
		}

		logger.L().Warn(ctx, "offset is beyond the end of mapping, but normalize fix is not applied",
			zap.Int64("offset", offset),
			zap.Uint64("mappingOffset", mapping.Offset),
			zap.Uint64("mappingEnd", mapping.Offset+mapping.Length),
			logger.WithBuildID(t.Metadata.BuildId.String()),
		)
	}

	return mapping, shift, nil
}
