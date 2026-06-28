package db

import (
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
)

// LayerSizesParams maps the orchestrator's synchronously-available logical layer
// sizes into params for SetEnvBuildLayerSizes. A zero value maps to NULL (e.g.
// the memfile logical size is 0 for filesystem-only snapshots).
func LayerSizesParams(buildID uuid.UUID, ls *orchestrator.LayerSizes) queries.SetEnvBuildLayerSizesParams {
	p := queries.SetEnvBuildLayerSizesParams{BuildID: buildID}
	if ls == nil {
		return p
	}

	p.MemfileLogicalSizeBytes = sizePtr(ls.GetMemfileLogicalSize())

	return p
}

func sizePtr(v uint64) *int64 {
	if v == 0 {
		return nil
	}

	i := int64(v)

	return &i
}
