//go:build linux

package sandbox

import (
	"cmp"
	"context"
	"fmt"
	"slices"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// DiffHeader resolves sync for every path except the memfd-dedup one,
// which resolves it from a goroutine so Pause can return before compare.
type DiffHeader = utils.SetOnce[*header.Header]

func NewResolvedDiffHeader(h *header.Header) *DiffHeader {
	d := utils.NewSetOnce[*header.Header]()
	_ = d.SetValue(h)

	return d
}

type Snapshot struct {
	MemfileDiff        build.Diff
	MemfileDiffHeader  *DiffHeader
	RootfsDiff         build.Diff
	RootfsDiffHeader   *DiffHeader
	Snapfile           template.File
	Metafile           template.File
	BuildID            uuid.UUID
	SchedulingMetadata *orchestrator.SnapshotSchedulingMetadata

	// Template block sizes captured sync at Pause time. They equal
	// MemfileDiffHeader.Metadata.BlockSize once that header resolves, but
	// are needed sync by NewUpload's compression validation — the dedup
	// memfile path produces a page-granular Diff.BlockSize() that doesn't
	// match the chunker-read granularity on restore.
	MemfileBlockSize uint64
	RootfsBlockSize  uint64

	cleanup *Cleanup
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}

type schedulingBuildContribution struct {
	buildID      uuid.UUID
	memfileBytes uint64
	rootfsBytes  uint64
}

func NewSnapshotSchedulingMetadata(memfileHeader, rootfsHeader *header.Header, limit int) *orchestrator.SnapshotSchedulingMetadata {
	if memfileHeader == nil || memfileHeader.Metadata == nil || rootfsHeader == nil || rootfsHeader.Metadata == nil {
		return nil
	}
	if limit < 0 {
		limit = 0
	}
	if limit > 128 {
		limit = 128
	}

	contributions := make(map[uuid.UUID]*schedulingBuildContribution)
	add := func(buildID uuid.UUID, memfileBytes, rootfsBytes uint64) {
		if buildID == uuid.Nil {
			return
		}
		c, ok := contributions[buildID]
		if !ok {
			c = &schedulingBuildContribution{buildID: buildID}
			contributions[buildID] = c
		}
		c.memfileBytes += memfileBytes
		c.rootfsBytes += rootfsBytes
	}

	for _, m := range memfileHeader.Mapping.All() {
		add(m.BuildId, m.Length, 0)
	}
	for _, m := range rootfsHeader.Mapping.All() {
		add(m.BuildId, 0, m.Length)
	}

	baseBuildID := memfileHeader.Metadata.BaseBuildId
	if baseBuildID == uuid.Nil {
		baseBuildID = rootfsHeader.Metadata.BaseBuildId
	}

	chain := make([]schedulingBuildContribution, 0, len(contributions))
	for _, c := range contributions {
		chain = append(chain, *c)
	}
	slices.SortFunc(chain, func(a, b schedulingBuildContribution) int {
		aBytes := a.memfileBytes + a.rootfsBytes
		bBytes := b.memfileBytes + b.rootfsBytes
		if aBytes == bBytes {
			return cmp.Compare(a.buildID.String(), b.buildID.String())
		}

		return cmp.Compare(bBytes, aBytes)
	})
	parentBuildID := memfileHeader.Metadata.BuildId
	if parentBuildID == uuid.Nil {
		parentBuildID = rootfsHeader.Metadata.BuildId
	}
	for i, c := range chain {
		if c.buildID == parentBuildID {
			copy(chain[1:i+1], chain[:i])
			chain[0] = c

			break
		}
	}
	if limit == 0 {
		chain = nil
	} else if len(chain) > limit {
		chain = chain[:limit]
		if baseBuildID != uuid.Nil {
			hasBase := false
			for _, c := range chain {
				if c.buildID == baseBuildID {
					hasBase = true

					break
				}
			}
			if !hasBase {
				if c, ok := contributions[baseBuildID]; ok {
					chain[len(chain)-1] = *c
				} else {
					chain[len(chain)-1] = schedulingBuildContribution{buildID: baseBuildID}
				}
			}
		}
	}

	res := &orchestrator.SnapshotSchedulingMetadata{
		BaseBuildId:         baseBuildID.String(),
		Generation:          max(memfileHeader.Metadata.Generation, rootfsHeader.Metadata.Generation) + 1,
		MemfileMappingCount: uint64(memfileHeader.Mapping.Len()),
		RootfsMappingCount:  uint64(rootfsHeader.Mapping.Len()),
		ChainBuildIds:       make([]string, 0, len(chain)),
		ChainMemfileBytes:   make([]uint64, 0, len(chain)),
		ChainRootfsBytes:    make([]uint64, 0, len(chain)),
	}
	for _, c := range chain {
		res.ChainBuildIds = append(res.ChainBuildIds, c.buildID.String())
		res.ChainMemfileBytes = append(res.ChainMemfileBytes, c.memfileBytes)
		res.ChainRootfsBytes = append(res.ChainRootfsBytes, c.rootfsBytes)
	}

	return res
}
