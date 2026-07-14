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
	// MemorySnapshot bundles the memfile diff, its header, and block size. It is
	// empty (NoDiff) for filesystem-only snapshots (see FilesystemSnapshot).
	MemorySnapshot MemorySnapshot

	RootfsDiff         build.Diff
	RootfsDiffHeader   *DiffHeader
	Snapfile           template.File
	Metafile           template.File
	BuildID            uuid.UUID
	SchedulingMetadata *orchestrator.SchedulingMetadata

	// FilesystemSnapshot is true for filesystem-only snapshots: the memfile diff
	// is empty (NoDiff) and the memfile, memfile header, and snapfile are not
	// uploaded. It records the decision made at pause time, which can't be
	// inferred from the diff shape — a memory snapshot with zero dirty pages also
	// produces a NoDiff memfile but still needs its snapfile uploaded.
	FilesystemSnapshot bool

	// FilesystemSibling is the filesystem-only sibling snapshot derived from
	// this memory pause when requested via WithFilesystemSibling: a separate
	// build sharing this snapshot's rootfs data via header references. Its
	// upload must run after this snapshot's upload has finished.
	FilesystemSibling *Snapshot

	// RootfsBlockSize is captured sync at Pause time — needed sync by NewUpload's
	// compression validation. (The memfile block size lives in
	// MemorySnapshot.BlockSize.)
	RootfsBlockSize uint64

	cleanup *Cleanup
}

// NewFilesystemOnlySnapshot wraps a host-generated rootfs layer for upload.
// It owns rootfsDiff and metafile until the template cache accepts them.
func NewFilesystemOnlySnapshot(
	ctx context.Context,
	buildID uuid.UUID,
	rootfsDiff build.Diff,
	rootfsHeader *header.Header,
	metafile template.File,
) *Snapshot {
	cleanup := NewCleanup()
	cleanup.AddNoContext(ctx, rootfsDiff.Close)
	cleanup.AddNoContext(ctx, metafile.Close)

	return &Snapshot{
		MemorySnapshot: MemorySnapshot{
			Diff:       build.Diff(&build.NoDiff{}),
			DiffHeader: NewResolvedDiffHeader(nil),
		},
		RootfsDiff:         rootfsDiff,
		RootfsDiffHeader:   NewResolvedDiffHeader(rootfsHeader),
		Snapfile:           &template.NoopFile{},
		Metafile:           metafile,
		BuildID:            buildID,
		SchedulingMetadata: scheduling.FromHeaders(buildID, nil, rootfsHeader, 0),
		FilesystemSnapshot: true,
		RootfsBlockSize:    rootfsHeader.Metadata.BlockSize,
		cleanup:            cleanup,
	}
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}
