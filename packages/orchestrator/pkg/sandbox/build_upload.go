package sandbox

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

// BuildUploader uploads a paused snapshot's files and headers. Returns the
// serialized header bytes for peer transition (nil for uncompressed).
type BuildUploader interface {
	Upload(ctx context.Context) (memfileHeader, rootfsHeader []byte, err error)
}

func NewBuildUploader(ctx context.Context, snapshot *Snapshot, persistence storage.StorageProvider, paths storage.Paths, cfg storage.CompressConfig, ff *featureflags.Client, useCase string) BuildUploader {
	base := buildUploader{
		paths:       paths,
		persistence: persistence,
		snapshot:    snapshot,
	}

	memCfg := storage.ResolveCompressConfig(ctx, cfg, ff, storage.MemfileName, useCase)
	rootfsCfg := storage.ResolveCompressConfig(ctx, cfg, ff, storage.RootfsName, useCase)

	if !memCfg.IsCompressionEnabled() && !rootfsCfg.IsCompressionEnabled() {
		return &uncompressedUploader{buildUploader: base}
	}

	return &compressedUploader{
		buildUploader:        base,
		memCompressConfig:    memCfg,
		rootfsCompressConfig: rootfsCfg,
	}
}

type buildUploader struct {
	paths       storage.Paths
	persistence storage.StorageProvider
	snapshot    *Snapshot
}
