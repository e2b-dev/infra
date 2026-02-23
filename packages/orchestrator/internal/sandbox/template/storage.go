package template

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"golang.org/x/sync/errgroup"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/build"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	oldMemfileHugePageSize = 2 << 20 // 2 MiB
	oldRootfsBlockSize     = 2 << 11 // 4 KiB
)

type Storage struct {
	header *header.Header
	source *build.File
}

func isKnownDiffType(diffType build.DiffType) bool {
	return diffType == build.Memfile || diffType == build.Rootfs
}

// loadHeaderV3 loads a v3 header from the standard (uncompressed) path.
// Returns (nil, nil) if not found.
func loadHeaderV3(ctx context.Context, persistence storage.StorageProvider, path string) (*header.Header, error) {
	blob, err := persistence.OpenBlob(ctx, path)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, nil
		}

		return nil, err
	}

	return header.Deserialize(ctx, blob)
}

// loadV4Header loads a v4 header (LZ4 compressed), decompresses, and deserializes it.
// Returns (nil, nil) if not found.
func loadV4Header(ctx context.Context, persistence storage.StorageProvider, path string) (*header.Header, error) {
	data, err := storage.LoadBlob(ctx, persistence, path)
	if err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return nil, nil
		}

		return nil, err
	}

	return header.DeserializeV4(data)
}

// loadHeaderPreferV4 fetches both v3 and v4 headers in parallel,
// preferring the v4 (compressed) header if available.
func loadHeaderPreferV4(ctx context.Context, persistence storage.StorageProvider, buildId string, fileType build.DiffType) (*header.Header, error) {
	files := storage.TemplateFiles{BuildID: buildId}
	v3Path := files.HeaderPath(string(fileType))
	v4Path := files.CompressedHeaderPath(string(fileType))

	var v3Header, v4Header *header.Header
	var v3Err, v4Err error

	eg, egCtx := errgroup.WithContext(ctx)
	eg.Go(func() error {
		v3Header, v3Err = loadHeaderV3(egCtx, persistence, v3Path)

		return nil
	})
	eg.Go(func() error {
		v4Header, v4Err = loadV4Header(egCtx, persistence, v4Path)

		return nil
	})
	_ = eg.Wait()

	if v4Err == nil && v4Header != nil {
		return v4Header, nil
	}
	if v3Err == nil && v3Header != nil {
		return v3Header, nil
	}
	if v4Err != nil {
		return nil, v4Err
	}
	if v3Err != nil {
		return nil, v3Err
	}

	return nil, nil
}

func NewStorage(
	ctx context.Context,
	store *build.DiffStore,
	buildId string,
	fileType build.DiffType,
	h *header.Header,
	flags *featureflags.Client,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
) (*Storage, error) {
	// Read chunker config from feature flag.
	chunkerCfg := flags.JSONFlag(ctx, featureflags.ChunkerConfigFlag).AsValueMap()
	useCompressedAssets := chunkerCfg.Get("useCompressedAssets").BoolValue()

	if h == nil {
		if !isKnownDiffType(fileType) {
			return nil, build.UnknownDiffTypeError{DiffType: fileType}
		}

		var err error
		if useCompressedAssets {
			h, err = loadHeaderPreferV4(ctx, persistence, buildId, fileType)
		} else {
			files := storage.TemplateFiles{BuildID: buildId}
			h, err = loadHeaderV3(ctx, persistence, files.HeaderPath(string(fileType)))
		}
		if err != nil {
			return nil, err
		}
	}

	// If we can't find the diff header in storage, we try to find the "old" style template without a header as a fallback.
	if h == nil {
		objectPath := buildId + "/" + string(fileType)
		if !isKnownDiffType(fileType) {
			return nil, build.UnknownDiffTypeError{DiffType: fileType}
		}
		object, err := persistence.OpenFramedFile(ctx, objectPath)
		if err != nil {
			return nil, err
		}

		size, err := object.Size(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get object size: %w", err)
		}

		id, err := uuid.Parse(buildId)
		if err != nil {
			return nil, fmt.Errorf("failed to parse build id: %w", err)
		}

		// TODO: This is a workaround for the old style template without a header.
		// We don't know the block size of the old style template, so we set it manually.
		var blockSize uint64
		switch fileType {
		case build.Memfile:
			blockSize = oldMemfileHugePageSize
		case build.Rootfs:
			blockSize = oldRootfsBlockSize
		default:
			return nil, fmt.Errorf("unsupported file type: %s", fileType)
		}

		h, err = header.NewHeader(&header.Metadata{
			// The version is always 1 for the old style template without a header.
			Version:     1,
			BuildId:     id,
			BaseBuildId: id,
			Size:        uint64(size),
			BlockSize:   blockSize,
			Generation:  1,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create header for old style template: %w", err)
		}
	}

	b := build.NewFile(h, store, fileType, persistence, metrics, flags)

	return &Storage{
		source: b,
		header: h,
	}, nil
}

func (d *Storage) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	return d.source.ReadAt(ctx, p, off)
}

func (d *Storage) Size(_ context.Context) (int64, error) {
	return int64(d.header.Metadata.Size), nil
}

func (d *Storage) BlockSize() int64 {
	return int64(d.header.Metadata.BlockSize)
}

func (d *Storage) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	return d.source.Slice(ctx, off, length)
}

func (d *Storage) Header() *header.Header {
	return d.header
}

func (d *Storage) Close() error {
	return nil
}
