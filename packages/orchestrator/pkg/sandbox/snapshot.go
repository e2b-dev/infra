package sandbox

import (
	"context"
	"fmt"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Snapshot struct {
	// Paths.BuildID identifies this snapshot in storage.
	Paths storage.Paths

	MemfileDiff       build.Diff
	MemfileDiffHeader *header.Header
	RootfsDiff        build.Diff
	RootfsDiffHeader  *header.Header
	Snapfile          template.File
	Metafile          template.File

	// ParentBuildID is the immediate parent layer (uuid.Nil for base layers
	// where the source template's buildID equals self's). Used by the upload
	// pipeline to wait for the parent's final header before finalizing self's.
	ParentBuildID uuid.UUID

	cleanup *Cleanup
}

// Upload uploads the snapshot's data files to object storage, finalizes each
// per-file V4 header (waiting on the parent's final header for compressed
// builds), and swaps the finalized headers into the templateCache build.File.
//
// Returns serialized memfile / rootfs header bytes (both nil for uncompressed
// V3 builds). The self build ID and parent are read from s.Paths and
// s.ParentBuildID, populated by Sandbox.Pause.
func (s *Snapshot) Upload(
	ctx context.Context,
	persistence storage.StorageProvider,
	cfg storage.CompressConfig,
	ff *featureflags.Client,
	useCase string,
	coord *UploadCoordinator,
) (memfileHdr, rootfsHdr []byte, err error) {
	memCfg := storage.ResolveCompressConfig(ctx, cfg, ff, storage.MemfileName, useCase)
	rootfsCfg := storage.ResolveCompressConfig(ctx, cfg, ff, storage.RootfsName, useCase)

	if !memCfg.IsCompressionEnabled() && !rootfsCfg.IsCompressionEnabled() {
		return (&uncompressedUploader{persistence: persistence, snapshot: s}).upload(ctx)
	}

	return (&compressedUploader{
		persistence: persistence,
		snapshot:    s,
		coord:       coord,
		memCfg:      memCfg,
		rootfsCfg:   rootfsCfg,
	}).upload(ctx)
}

// SelfBuildID returns this snapshot's build ID parsed from Paths.BuildID.
func (s *Snapshot) SelfBuildID() uuid.UUID {
	return uuid.MustParse(s.Paths.BuildID)
}

func (s *Snapshot) Close(ctx context.Context) error {
	err := s.cleanup.Run(ctx)
	if err != nil {
		return fmt.Errorf("error cleaning up snapshot: %w", err)
	}

	return nil
}
