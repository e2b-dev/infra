package sandbox

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
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

func (s *Snapshot) Upload(
	ctx context.Context,
	persistence storage.StorageProvider,
	templateFiles storage.TemplateFiles,
) error {
	var memfilePath *string
	switch r := s.MemfileDiff.(type) {
	case *build.NoDiff:
	default:
		memfileLocalPath, err := r.CachePath()
		if err != nil {
			return fmt.Errorf("error getting memfile diff path: %w", err)
		}

		memfilePath = &memfileLocalPath
	}

	var rootfsPath *string
	switch r := s.RootfsDiff.(type) {
	case *build.NoDiff:
	default:
		rootfsLocalPath, err := r.CachePath()
		if err != nil {
			return fmt.Errorf("error getting rootfs diff path: %w", err)
		}

		rootfsPath = &rootfsLocalPath
	}

	templateBuild := NewTemplateBuild(
		s.MemfileDiffHeader,
		s.RootfsDiffHeader,
		persistence,
		templateFiles,
	)

	if err := templateBuild.Upload(
		ctx,
		s.Metafile.Path(),
		s.Snapfile.Path(),
		memfilePath,
		rootfsPath,
	); err != nil {
		return fmt.Errorf("error uploading template files: %w", err)
	}

	return nil
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}
