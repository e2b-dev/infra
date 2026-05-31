package scheduling

import (
	"cmp"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	chainLimit = 32
	chunkSize  = 2 << 20
)

type buildContribution struct {
	buildID             uuid.UUID
	memfileBytes        uint64
	memfileChunks       map[uint64]struct{}
	memfileMappingCount uint32
	rootfsBytes         uint64
	rootfsChunks        map[uint64]struct{}
	rootfsMappingCount  uint32
}

func FromHeaders(memfileHeader, rootfsHeader *header.Header) *orchestrator.SchedulingMetadata {
	if memfileHeader == nil || memfileHeader.Metadata == nil || rootfsHeader == nil || rootfsHeader.Metadata == nil {
		return nil
	}

	contributions := make(map[uuid.UUID]*buildContribution)
	add := func(buildID uuid.UUID, storageOffset, length uint64, isMemfile bool) {
		if buildID == uuid.Nil || length == 0 {
			return
		}
		c, ok := contributions[buildID]
		if !ok {
			c = &buildContribution{
				buildID:       buildID,
				memfileChunks: make(map[uint64]struct{}),
				rootfsChunks:  make(map[uint64]struct{}),
			}
			contributions[buildID] = c
		}

		startChunk := storageOffset / chunkSize
		endChunk := (storageOffset + length - 1) / chunkSize
		if isMemfile {
			c.memfileBytes += length
			c.memfileMappingCount++
			for i := startChunk; i <= endChunk; i++ {
				c.memfileChunks[i] = struct{}{}
			}
		} else {
			c.rootfsBytes += length
			c.rootfsMappingCount++
			for i := startChunk; i <= endChunk; i++ {
				c.rootfsChunks[i] = struct{}{}
			}
		}
	}

	for _, m := range memfileHeader.Mapping.All() {
		add(m.BuildId, m.BuildStorageOffset, m.Length, true)
	}
	for _, m := range rootfsHeader.Mapping.All() {
		add(m.BuildId, m.BuildStorageOffset, m.Length, false)
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
		Generation:           max(memfileHeader.Metadata.Generation, rootfsHeader.Metadata.Generation) + 1,
		ChainBuildIds:        make([]string, 0, len(chain)),
		MemfileLogicalBytes:  make([]uint64, 0, len(chain)),
		MemfileChunkCounts:   make([]uint32, 0, len(chain)),
		MemfileMappingCounts: make([]uint32, 0, len(chain)),
		RootfsLogicalBytes:   make([]uint64, 0, len(chain)),
		RootfsChunkCounts:    make([]uint32, 0, len(chain)),
		RootfsMappingCounts:  make([]uint32, 0, len(chain)),
	}
	for _, c := range chain {
		res.ChainBuildIds = append(res.ChainBuildIds, c.buildID.String())
		res.MemfileLogicalBytes = append(res.MemfileLogicalBytes, c.memfileBytes)
		res.MemfileChunkCounts = append(res.MemfileChunkCounts, uint32(len(c.memfileChunks)))
		res.MemfileMappingCounts = append(res.MemfileMappingCounts, c.memfileMappingCount)
		res.RootfsLogicalBytes = append(res.RootfsLogicalBytes, c.rootfsBytes)
		res.RootfsChunkCounts = append(res.RootfsChunkCounts, uint32(len(c.rootfsChunks)))
		res.RootfsMappingCounts = append(res.RootfsMappingCounts, c.rootfsMappingCount)
	}

	return res
}
