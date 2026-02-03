package build

import (
	"context"
	"fmt"
	"io"

	"github.com/google/uuid"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type File struct {
	header      *header.Header
	store       *DiffStore
	fileType    DiffType
	persistence storage.StorageProvider
	metrics     blockmetrics.Metrics
}

func NewFile(
	header *header.Header,
	store *DiffStore,
	fileType DiffType,
	persistence storage.StorageProvider,
	metrics blockmetrics.Metrics,
) *File {
	return &File{
		header:      header,
		store:       store,
		fileType:    fileType,
		persistence: persistence,
		metrics:     metrics,
	}
}

func (b *File) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	for n < len(p) {
		mappedToBuild, err := b.header.GetShiftedMapping(ctx, off+int64(n))
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		remainingReadLength := int64(len(p)) - int64(n)
		readLength := min(int64(mappedToBuild.Length), remainingReadLength)

		if readLength <= 0 {
			logger.L().Error(ctx, fmt.Sprintf(
				"(%d bytes left to read, off %d) reading %d bytes from %+v/%+v: [%d:] -> [%d:%d] <> %d (mapped length: %d, remaining read length: %d)\n>>> EOF\n",
				len(p)-n,
				off,
				readLength,
				mappedToBuild.BuildId,
				b.fileType,
				mappedToBuild.Offset,
				n,
				int64(n)+readLength,
				n,
				mappedToBuild.Length,
				remainingReadLength,
			))

			return n, io.EOF
		}

		// Skip reading when the uuid is nil.
		// We will use this to handle base builds that are already diffs.
		// The passed slice p must start as empty, otherwise we would need to copy the empty values there.
		if mappedToBuild.BuildId == uuid.Nil {
			n += int(readLength)

			continue
		}

		mappedBuild, err := b.getBuild(ctx, mappedToBuild.BuildId)
		if err != nil {
			return 0, fmt.Errorf("failed to get build: %w", err)
		}

		buildN, err := mappedBuild.ReadAt(ctx,
			p[n:int64(n)+readLength],
			int64(mappedToBuild.Offset),
			mappedToBuild.FrameTable,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += buildN
	}

	return n, nil
}

// The slice access must be in the predefined blocksize of the build.
// Address spaces: V=Virtual (guest offset), U=Uncompressed storage offset
func (b *File) Slice(ctx context.Context, off, _ int64) ([]byte, error) {
	mappedBuild, err := b.header.GetShiftedMapping(ctx, off)
	if err != nil {
		return nil, fmt.Errorf("failed to get mapping: %w", err)
	}

	// Pass empty huge page when the build id is nil (sparse/zero regions).
	if mappedBuild.BuildId == uuid.Nil {
		return header.EmptyHugePage, nil
	}

	build, err := b.getBuild(ctx, mappedBuild.BuildId)
	if err != nil {
		return nil, fmt.Errorf("failed to get build: %w", err)
	}

	data, err := build.Slice(ctx, int64(mappedBuild.Offset), int64(b.header.Metadata.BlockSize), mappedBuild.FrameTable)
	if err != nil {
		return nil, err
	}

	return data, nil
}

func (b *File) getBuild(ctx context.Context, buildId uuid.UUID) (Diff, error) {
	// Create or retrieve StorageDiff for the target build from the cache.
	// The chunker is lazily initialized on first ReadAt/Slice using the frame table
	// from the mapping (passed to ReadAt/Slice by the caller).
	storageDiff, err := newStorageDiff(
		b.store.cachePath,
		buildId.String(),
		b.fileType,
		int64(b.header.Metadata.BlockSize),
		b.metrics,
		b.persistence,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create storage diff: %w", err)
	}

	source, err := b.store.Get(ctx, storageDiff)
	if err != nil {
		return nil, fmt.Errorf("failed to get build from store: %w", err)
	}

	return source, nil
}
