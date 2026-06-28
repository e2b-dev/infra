//go:build linux

package sandbox

import (
	"context"
	"fmt"

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

	// RootfsBlockSize is captured sync at Pause time — needed sync by NewUpload's
	// compression validation. (The memfile block size lives in
	// MemorySnapshot.BlockSize.)
	RootfsBlockSize uint64

	cleanup *Cleanup
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}

// SyncLayerSizes returns the layer sizes available without waiting on the async
// memfile dedup header: the synchronously-built rootfs mapped/diff sizes and the
// memfile logical size. The memfile mapped/diff sizes are intentionally excluded
// because deriving them would block on the dedup header (potentially tens of
// seconds); they are written to the memfile data object's metadata instead.
func (s *Snapshot) SyncLayerSizes(ctx context.Context) (*orchestrator.LayerSizes, error) {
	ls := &orchestrator.LayerSizes{}

	// The rootfs diff header is resolved synchronously at Pause time, so this
	// Wait returns immediately.
	rootfsHeader, err := s.RootfsDiffHeader.WaitWithContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("wait rootfs diff header: %w", err)
	}
	if rootfsHeader != nil {
		ls.RootfsMappedSize = rootfsHeader.Mapping.MappedBytes()
		ls.RootfsDiffSize = rootfsHeader.Mapping.BytesByBuild()[rootfsHeader.Metadata.BuildId]
	}

	if !s.FilesystemSnapshot {
		ls.MemfileLogicalSize = s.MemorySnapshot.LogicalSize()
	}

	return ls, nil
}

// LogicalSize returns the memfile's logical (virtual device) size from its base
// header, available synchronously at Pause time. Returns 0 for a filesystem-only
// snapshot, which has no memfile.
func (m MemorySnapshot) LogicalSize() uint64 {
	if m.header == nil {
		return 0
	}

	return m.header.Metadata.Size
}
