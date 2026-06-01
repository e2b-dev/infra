package scheduling

import (
	"cmp"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const chainLimit = 32

type buildContribution struct {
	buildID             uuid.UUID
	memfileBytes        uint64
	memfileMappingCount uint32
	rootfsBytes         uint64
	rootfsMappingCount  uint32
}

func FromHeaders(memfileHeader, rootfsHeader *header.Header) *orchestrator.SchedulingMetadata {
	if memfileHeader == nil || memfileHeader.Metadata == nil || rootfsHeader == nil || rootfsHeader.Metadata == nil {
		return nil
	}

	contributions := make(map[uuid.UUID]*buildContribution)
	add := func(buildID uuid.UUID, length uint64, isMemfile bool) {
		if buildID == uuid.Nil || length == 0 {
			return
		}
		c, ok := contributions[buildID]
		if !ok {
			c = &buildContribution{buildID: buildID}
			contributions[buildID] = c
		}
		if isMemfile {
			c.memfileBytes += length
			c.memfileMappingCount++
		} else {
			c.rootfsBytes += length
			c.rootfsMappingCount++
		}
	}

	for _, m := range memfileHeader.Mapping.All() {
		add(m.BuildId, m.Length, true)
	}
	for _, m := range rootfsHeader.Mapping.All() {
		add(m.BuildId, m.Length, false)
	}

	baseBuildID := memfileHeader.Metadata.BaseBuildId
	if baseBuildID == uuid.Nil {
		baseBuildID = rootfsHeader.Metadata.BaseBuildId
	}

	chain := make([]buildContribution, 0, len(contributions))
	for _, c := range contributions {
		chain = append(chain, *c)
	}
	slices.SortFunc(chain, func(a, b buildContribution) int {
		aBytes := a.memfileBytes + a.rootfsBytes
		bBytes := b.memfileBytes + b.rootfsBytes
		if aBytes == bBytes {
			return cmp.Compare(a.buildID.String(), b.buildID.String())
		}

		return cmp.Compare(bBytes, aBytes)
	})
	if len(chain) > chainLimit {
		chain = chain[:chainLimit]
	}

	res := &orchestrator.SchedulingMetadata{
		BaseBuildId:          baseBuildID.String(),
		Generation:           max(memfileHeader.Metadata.Generation, rootfsHeader.Metadata.Generation),
		MemfileSize:          memfileHeader.Metadata.Size,
		RootfsSize:           rootfsHeader.Metadata.Size,
		ChainBuildIds:        make([]string, 0, len(chain)),
		MemfileLogicalBytes:  make([]uint64, 0, len(chain)),
		MemfileMappingCounts: make([]uint32, 0, len(chain)),
		RootfsLogicalBytes:   make([]uint64, 0, len(chain)),
		RootfsMappingCounts:  make([]uint32, 0, len(chain)),
	}
	for _, c := range chain {
		res.ChainBuildIds = append(res.ChainBuildIds, c.buildID.String())
		res.MemfileLogicalBytes = append(res.MemfileLogicalBytes, c.memfileBytes)
		res.MemfileMappingCounts = append(res.MemfileMappingCounts, c.memfileMappingCount)
		res.RootfsLogicalBytes = append(res.RootfsLogicalBytes, c.rootfsBytes)
		res.RootfsMappingCounts = append(res.RootfsMappingCounts, c.rootfsMappingCount)
	}

	return res
}
