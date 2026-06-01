//go:build linux

package sandbox

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/scheduling"
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
	SchedulingMetadata *orchestrator.SchedulingMetadata

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

// NewSnapshotSchedulingMetadata derives the metadata from the new snapshot's
// diff headers so the freshly created build and its contributions are included.
func NewSnapshotSchedulingMetadata(ctx context.Context, memfileDiffHeader, rootfsDiffHeader *DiffHeader) *orchestrator.SchedulingMetadata {
	memfileHeader, memfileErr := memfileDiffHeader.WaitWithContext(ctx)
	rootfsHeader, rootfsErr := rootfsDiffHeader.WaitWithContext(ctx)
	if memfileErr != nil || rootfsErr != nil {
		return nil
	}

	return scheduling.FromHeaders(memfileHeader, rootfsHeader)
}
