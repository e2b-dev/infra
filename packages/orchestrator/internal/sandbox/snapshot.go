package sandbox

import (
	"context"
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
	Snapfile          template.File
	Metafile          template.File

	cleanup *Cleanup
}

// UploadSingleLayer uploads all data files and headers for this snapshot in one
// pass. Use for single-layer scenarios (e.g., sandbox pause) where no coordination
// with other layers is needed. Parent frame tables are preserved in the header
// from previous builds.
func (s *Snapshot) UploadSingleLayer(
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

	uploadErrCh := templateBuild.Upload(
		ctx,
		s.Metafile.Path(),
		s.Snapfile.Path(),
		memfilePath,
		rootfsPath,
	)

	// Wait for the upload to finish
	uploadErr := <-uploadErrCh
	if uploadErr != nil {
		return fmt.Errorf("error uploading template build: %w", uploadErr)
	}

	return nil
}

// UploadDataFilesResult contains the results of uploading data files.
type UploadDataFilesResult struct {
	TemplateBuild     *TemplateBuild
	RootfsFrameTable  *storage.FrameTable
	MemfileFrameTable *storage.FrameTable
}

// UploadDataFiles uploads data files (metadata, snapfile, memfile, rootfs) for
// multi-layer builds. Returns the TemplateBuild and frame tables so the caller
// can coordinate with other layers before finalizing headers.
func (s *Snapshot) UploadDataFiles(
	ctx context.Context,
	persistence storage.StorageProvider,
	templateFiles storage.TemplateFiles,
) (*UploadDataFilesResult, error) {
	var memfilePath *string
	if _, isNoDiff := s.MemfileDiff.(*build.NoDiff); !isNoDiff {
		p, err := s.MemfileDiff.CachePath()
		if err != nil {
			return nil, fmt.Errorf("error getting memfile diff path: %w", err)
		}
		memfilePath = &p
	}

	var rootfsPath *string
	if _, isNoDiff := s.RootfsDiff.(*build.NoDiff); !isNoDiff {
		p, err := s.RootfsDiff.CachePath()
		if err != nil {
			return nil, fmt.Errorf("error getting rootfs diff path: %w", err)
		}
		rootfsPath = &p
	}

	templateBuild := NewTemplateBuild(
		s.MemfileDiffHeader,
		s.RootfsDiffHeader,
		persistence,
		templateFiles,
	)

	dataResult, err := templateBuild.UploadData(
		ctx,
		s.Metafile.Path(),
		s.Snapfile.Path(),
		memfilePath,
		rootfsPath,
	)
	if err != nil {
		return nil, fmt.Errorf("error uploading snapshot data: %w", err)
	}

	return &UploadDataFilesResult{
		TemplateBuild:     templateBuild,
		RootfsFrameTable:  dataResult.RootfsFrameTable,
		MemfileFrameTable: dataResult.MemfileFrameTable,
	}, nil
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}
