package scheduling

import (
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// chainLimit caps how many build IDs are reported per artifact.
const chainLimit = 64

// FromHeaders reports, per artifact, the deduplicated build IDs whose data the
// header references, plus the base (root) and the final/current build. buildID
// is the final layer; the snapshot path passes the new build there because its
// memfile header may not be resolved yet (so it derives the rest from the
// resolved parent headers). The lists are sorted for determinism — order is
// not significant for affinity matching.
func FromHeaders(buildID uuid.UUID, memfileHeader, rootfsHeader *header.Header) *orchestrator.SchedulingMetadata {
	if memfileHeader == nil || memfileHeader.Metadata == nil || rootfsHeader == nil || rootfsHeader.Metadata == nil {
		return nil
	}

	base := memfileHeader.Metadata.BaseBuildId
	if base == uuid.Nil {
		base = rootfsHeader.Metadata.BaseBuildId
	}

	return &orchestrator.SchedulingMetadata{
		BaseBuildId:     base.String(),
		BuildId:         buildID.String(),
		MemfileBuildIds: buildIDs(memfileHeader, buildID),
		RootfsBuildIds:  buildIDs(rootfsHeader, buildID),
	}
}

func buildIDs(h *header.Header, extra uuid.UUID) []string {
	seen := make(map[uuid.UUID]struct{})
	for _, m := range h.Mapping.All() {
		if m.BuildId != uuid.Nil {
			seen[m.BuildId] = struct{}{}
		}
	}
	if extra != uuid.Nil {
		seen[extra] = struct{}{}
	}

	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id.String())
	}
	slices.Sort(ids)
	if len(ids) > chainLimit {
		ids = ids[:chainLimit]
	}

	return ids
}
