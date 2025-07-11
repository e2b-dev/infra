package sandbox

import (
	"context"
	"errors"
	"fmt"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Snapshot struct {
	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          *template.LocalFileLink
}

func (s *Snapshot) Upload(
	ctx context.Context,
	persistance storage.StorageProvider,
	templateFiles storage.TemplateFiles,
) error {
	var memfilePath *string
	switch r := s.MemfileDiff.(type) {
	case *build.NoDiff:
		break
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
		break
	default:
		rootfsLocalPath, err := r.CachePath()
		if err != nil {
			return fmt.Errorf("error getting rootfs diff path: %w", err)
		}

		rootfsPath = &rootfsLocalPath
	}

	templateBuild := storage.NewTemplateBuild(
		s.MemfileDiffHeader,
		s.RootfsDiffHeader,
		persistance,
		templateFiles,
	)

	uploadErrCh := templateBuild.Upload(
		ctx,
		s.Snapfile.Path(),
		memfilePath,
		rootfsPath,
	)

	// Wait for the upload to finish
	err := <-uploadErrCh
	if err != nil {
		return fmt.Errorf("error uploading template build: %w", err)
	}
	return nil
}

func (s *Snapshot) Close(_ context.Context) error {
	var errs []error

	if err := s.MemfileDiff.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close memfile diff: %w", err))
	}

	if err := s.RootfsDiff.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close rootfs diff: %w", err))
	}

	if err := s.Snapfile.Close(); err != nil {
		errs = append(errs, fmt.Errorf("failed to close snapfile: %w", err))
	}

	return errors.Join(errs...)
}
