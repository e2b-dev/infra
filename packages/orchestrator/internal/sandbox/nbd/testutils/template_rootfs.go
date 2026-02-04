package testutils

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

func TemplateRootfs(ctx context.Context, buildID string) (*BuildDevice, *Cleaner, error) {
	var cleaner Cleaner

	files := storage.TemplateFiles{
		BuildID: buildID,
	}

	storage, err := storage.GetTemplateStorageProvider(ctx, nil)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to get storage provider: %w", err)
	}

	headerData, err := storage.GetBlob(ctx, files.StorageRootfsHeaderPath())
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to get header data: %w", err)
	}

	h, err := header.Deserialize(headerData)
	if err != nil {
		id, err := uuid.Parse(buildID)
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to parse build id: %w", err)
		}

		size, _, err := storage.Size(ctx, files.StorageRootfsPath())
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to get object size: %w", err)
		}

		h, err = header.NewHeader(&header.Metadata{
			BuildId:     id,
			BaseBuildId: id,
			Size:        uint64(size),
			Version:     1,
			BlockSize:   header.RootfsBlockSize,
			Generation:  1,
		}, nil)
		if err != nil {
			return nil, &cleaner, fmt.Errorf("failed to create header for rootfs without header: %w", err)
		}
	}

	diffCacheDir := filepath.Join(os.TempDir(), fmt.Sprintf("%s-rootfs.diff.cache-%s", buildID, uuid.New().String()))

	err = os.MkdirAll(diffCacheDir, 0o755)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create diff cache directory: %w", err)
	}

	cleaner.Add(func(context.Context) error {
		return os.RemoveAll(diffCacheDir)
	})

	flags, err := featureflags.NewClient()
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create feature flags client: %w", err)
	}

	store, err := build.NewDiffStore(
		cfg.Config{},
		flags,
		diffCacheDir,
		24*time.Hour,
		24*time.Hour,
	)
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create diff store: %w", err)
	}

	store.Start(ctx)

	cleaner.Add(func(context.Context) error {
		store.RemoveCache()

		return nil
	})

	cleaner.Add(func(context.Context) error {
		store.Close()

		return nil
	})

	m, err := metrics.NewMetrics(noop.NewMeterProvider())
	if err != nil {
		return nil, &cleaner, fmt.Errorf("failed to create metrics: %w", err)
	}

	buildDevice := NewBuildDevice(
		build.NewFile(h, store, build.Rootfs, storage, m),
		h,
		int64(h.Metadata.BlockSize),
	)

	return buildDevice, &cleaner, nil
}
