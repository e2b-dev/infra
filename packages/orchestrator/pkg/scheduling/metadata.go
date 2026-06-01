package scheduling

import (
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// chainLimit caps how many build IDs are reported in scheduling metadata.
const chainLimit = 64

// FromHeaders builds scheduling metadata for resume affinity. ChainBuildIds is
// the deduplicated union of build IDs referenced by the memfile and rootfs
// mappings, sorted for determinism (a membership set, not lineage order).
// Generation is the deeper of the two headers.
func FromHeaders(memfileHeader, rootfsHeader *header.Header) *orchestrator.SchedulingMetadata {
	if memfileHeader == nil || memfileHeader.Metadata == nil || rootfsHeader == nil || rootfsHeader.Metadata == nil {
		return nil
	}

	seen := make(map[uuid.UUID]struct{})
	for _, h := range [...]*header.Header{memfileHeader, rootfsHeader} {
		for _, m := range h.Mapping.All() {
			if m.BuildId != uuid.Nil {
				seen[m.BuildId] = struct{}{}
			}
		}
	}

	chain := make([]string, 0, len(seen))
	for id := range seen {
		chain = append(chain, id.String())
	}
	slices.Sort(chain)
	if len(chain) > chainLimit {
		chain = chain[:chainLimit]
	}

	baseBuildID := memfileHeader.Metadata.BaseBuildId
	if baseBuildID == uuid.Nil {
		baseBuildID = rootfsHeader.Metadata.BaseBuildId
	}

	return &orchestrator.SchedulingMetadata{
		BaseBuildId:   baseBuildID.String(),
		Generation:    max(memfileHeader.Metadata.Generation, rootfsHeader.Metadata.Generation),
		ChainBuildIds: chain,
	}
}
