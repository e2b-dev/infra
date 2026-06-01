package scheduling

import (
	"cmp"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// chainLimit caps how many build IDs are reported per artifact.
const chainLimit = 128

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

	memIDs, memDropped := buildIDs(memfileHeader, base, buildID)
	rootIDs, rootDropped := buildIDs(rootfsHeader, base, buildID)

	return &orchestrator.SchedulingMetadata{
		BaseBuildId:          base.String(),
		BuildId:              buildID.String(),
		MemfileBuildIds:      memIDs,
		RootfsBuildIds:       rootIDs,
		MemfileDroppedBuilds: uint32(memDropped),
		RootfsDroppedBuilds:  uint32(rootDropped),
	}
}

// buildIDs returns the build IDs referenced by the header (plus the outlined
// base and build, which are always kept), sorted by ID, and how many were
// dropped. Without an ordered chain there is no natural tail to trim, so when
// over chainLimit the lightest layers (fewest referenced bytes) are dropped.
func buildIDs(h *header.Header, base, build uuid.UUID) ([]string, int) {
	bytesByID := make(map[uuid.UUID]uint64)
	for _, m := range h.Mapping.All() {
		if m.BuildId != uuid.Nil {
			bytesByID[m.BuildId] += m.Length
		}
	}
	for _, id := range []uuid.UUID{base, build} {
		if id != uuid.Nil {
			if _, ok := bytesByID[id]; !ok {
				bytesByID[id] = 0
			}
		}
	}

	ids := make([]uuid.UUID, 0, len(bytesByID))
	for id := range bytesByID {
		ids = append(ids, id)
	}

	dropped := 0
	if len(ids) > chainLimit {
		slices.SortFunc(ids, func(a, b uuid.UUID) int {
			// Always keep the outlined endpoints, then the heaviest layers.
			if ka, kb := pinned(a, base, build), pinned(b, base, build); ka != kb {
				if ka {
					return -1
				}

				return 1
			}
			if c := cmp.Compare(bytesByID[b], bytesByID[a]); c != 0 {
				return c
			}

			return cmp.Compare(a.String(), b.String())
		})
		dropped = len(ids) - chainLimit
		ids = ids[:chainLimit]
	}

	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = id.String()
	}
	slices.Sort(out)

	return out, dropped
}

func pinned(id, base, build uuid.UUID) bool { return id == base || id == build }
