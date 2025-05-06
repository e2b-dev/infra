package header

import (
	"context"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type DiffMetadata struct {
	Dirty *bitset.BitSet
	Empty *bitset.BitSet

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
	)
	telemetry.ReportEvent(ctx, "created dirty mapping")

	emptyMappings := CreateMapping(
		&buildID,
		d.Empty,
		d.BlockSize,
	)
	telemetry.ReportEvent(ctx, "created empty mapping")

	mappings := MergeMappings(dirtyMappings, emptyMappings)
	telemetry.ReportEvent(ctx, "merge mappings")

	return mappings, nil
}
