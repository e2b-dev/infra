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
// header references (with their referenced bytes), plus the base (root) and the
// final/current build. buildID is the final layer; the snapshot path passes the
// new build there because its memfile header may not be resolved yet, so it
// derives the rest from the resolved parent headers and passes newMemfileBytes
// (a pre-dedup upper bound) for the new layer. Lists are sorted by build ID —
// order is not significant for affinity matching.
func FromHeaders(buildID uuid.UUID, memfileHeader, rootfsHeader *header.Header, newMemfileBytes uint64) *orchestrator.SchedulingMetadata {
	if memfileHeader == nil || memfileHeader.Metadata == nil || rootfsHeader == nil || rootfsHeader.Metadata == nil {
		return nil
	}

	base := memfileHeader.Metadata.BaseBuildId
	if base == uuid.Nil {
		base = rootfsHeader.Metadata.BaseBuildId
	}

	memIDs, memBytes, memDropped := artifactBuilds(memfileHeader, base, buildID, newMemfileBytes)
	rootIDs, rootBytes, rootDropped := artifactBuilds(rootfsHeader, base, buildID, 0)

	return &orchestrator.SchedulingMetadata{
		BaseBuildId:          base.String(),
		BuildId:              buildID.String(),
		MemfileBuildIds:      memIDs,
		RootfsBuildIds:       rootIDs,
		MemfileBuildBytes:    memBytes,
		RootfsBuildBytes:     rootBytes,
		MemfileDroppedBuilds: uint32(memDropped),
		RootfsDroppedBuilds:  uint32(rootDropped),
	}
}

// artifactBuilds returns the build IDs referenced by the header and their
// referenced bytes (aligned, sorted by ID), plus how many were dropped. The
// outlined base and build are always kept; build is added with injectBuildBytes
// when it is not already in the header (the not-yet-resolved new layer). Without
// an ordered chain there is no natural tail to trim, so over chainLimit the
// lightest layers are dropped.
func artifactBuilds(h *header.Header, base, build uuid.UUID, injectBuildBytes uint64) ([]string, []uint64, int) {
	bytesByID := make(map[uuid.UUID]uint64)
	for _, m := range h.Mapping.All() {
		if m.BuildId != uuid.Nil {
			bytesByID[m.BuildId] += m.Length
		}
	}
	if base != uuid.Nil {
		if _, ok := bytesByID[base]; !ok {
			bytesByID[base] = 0
		}
	}
	if build != uuid.Nil {
		if _, ok := bytesByID[build]; !ok {
			bytesByID[build] = injectBuildBytes
		}
	}

	ids := make([]uuid.UUID, 0, len(bytesByID))
	for id := range bytesByID {
		ids = append(ids, id)
	}

	dropped := 0
	if len(ids) > chainLimit {
		slices.SortFunc(ids, func(a, b uuid.UUID) int {
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

	slices.SortFunc(ids, func(a, b uuid.UUID) int { return cmp.Compare(a.String(), b.String()) })

	outIDs := make([]string, len(ids))
	outBytes := make([]uint64, len(ids))
	for i, id := range ids {
		outIDs[i] = id.String()
		outBytes[i] = bytesByID[id]
	}

	return outIDs, outBytes, dropped
}

func pinned(id, base, build uuid.UUID) bool { return id == base || id == build }
