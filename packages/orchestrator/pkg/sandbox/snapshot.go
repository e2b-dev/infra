package sandbox

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          template.File
	Metafile          template.File

	cleanup *Cleanup
}

// Upload uploads snapshot files to storage and returns serialized V4 header
// bytes for peer transition (nil for uncompressed builds).
func (s *Snapshot) Upload(
	ctx context.Context,
	persistence storage.StorageProvider,
	paths storage.Paths,
	cfg storage.CompressConfig,
	ff *featureflags.Client,
	useCase string,
) (memfileHdr, rootfsHdr []byte, err error) {
	uploader := NewBuildUploader(ctx, s, persistence, paths, cfg, ff, useCase)

	return uploader.Upload(ctx)
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}
