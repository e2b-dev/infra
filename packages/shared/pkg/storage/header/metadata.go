package header

import (
	"context"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var ignoreBuildID = uuid.Nil

type compressedBlockInfo struct {
	offset, size uint64
}

type DiffMetadata struct {
	Dirty     *bitset.BitSet
	Empty     *bitset.BitSet
	OffsetMap map[uint64]compressedBlockInfo

	BlockSize int64
}

func (d *DiffMetadata) CreateMapping(
	ctx context.Context,
	buildID uuid.UUID,
) (mapping []*BuildMap, e error) {
	dirtyMappings := CreateMapping(
		&buildID,
		d.Dirty,
		d.BlockSize,
		d.OffsetMap,
	)
	telemetry.ReportEvent(ctx, "created dirty mapping")

	emptyMappings := CreateMapping(&ignoreBuildID, d.Empty, d.BlockSize, nil)
	telemetry.ReportEvent(ctx, "created empty mapping")

	mappings := MergeMappings(dirtyMappings, emptyMappings)
	telemetry.ReportEvent(ctx, "merge mappings")

	return mappings, nil
}
