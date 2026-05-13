//go:build linux

package sandbox

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type SnapshotDiffStats struct {
	DirtyBytes int64
	EmptyBytes int64
	TotalBytes int64
}

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	MemfileDiffStats  SnapshotDiffStats
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	RootfsDiffStats   SnapshotDiffStats
	Snapfile          template.File
	Metafile          template.File
	BuildID           uuid.UUID

	cleanup *Cleanup
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}
