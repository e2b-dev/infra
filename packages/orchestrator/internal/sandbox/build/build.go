package build

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"

	"github.com/google/uuid"
	"go.uber.org/zap"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block/metrics"
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

func min(a, b int64) int64 {
	if a < b {
		return a
	}

	return b
}

func (b *File) ReadAt(p []byte, off int64) (n int, err error) {
	for n < len(p) {
		mappedOffset, mappedLength, buildID, err := b.header.GetShiftedMapping(off + int64(n))
		if err != nil {
			return 0, fmt.Errorf("failed to get mapping: %w", err)
		}

		remainingReadLength := int64(len(p)) - int64(n)

		readLength := min(mappedLength, remainingReadLength) // todo: is this still correct?

		if readLength <= 0 {
			zap.L().Error(fmt.Sprintf(
				"(%d bytes left to read, off %d) reading %d bytes from %+v/%+v: [%d:] -> [%d:%d] <> %d (mapped length: %d, remaining read length: %d)\n>>> EOF\n",
				len(p)-n,
				off,
				readLength,
				buildID,
				b.fileType,
				mappedOffset,
				n,
				int64(n)+readLength,
				n,
				mappedLength,
				remainingReadLength,
			))

			return n, io.EOF
		}

		// Skip reading when the uuid is nil.
		// We will use this to handle base builds that are already diffs.
		// The passed slice p must start as empty, otherwise we would need to copy the empty values there.
		if *buildID == uuid.Nil {
			n += int(readLength)

			continue
		}

		mappedBuild, err := b.getBuild(buildID)
		if err != nil {
			return 0, fmt.Errorf("failed to get build: %w", err)
		}

		compressed := make([]byte, mappedLength)
		buildN, err := mappedBuild.ReadAt(
			compressed,
			mappedOffset,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		uncompressed, err := decompress(compressed)
		if err != nil {
			return 0, fmt.Errorf("failed to decompress: %w", err)
		}

		copy(p[n:int64(n)+int64(len(uncompressed))], uncompressed)
		n += buildN
	}

	return n, nil
}

func decompress(compressed []byte) ([]byte, error) {
	reader := bytes.NewReader(compressed)
	decompressor, err := gzip.NewReader(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to create decompressor: %w", err)
	}
	defer decompressor.Close()

	return io.ReadAll(decompressor)
}

// The slice access must be in the predefined blocksize of the build.
func (b *File) Slice(off, length int64) ([]byte, error) {
	if uint64(length) != b.header.Metadata.BlockSize {
		return nil, fmt.Errorf("invalid header size: %d != %d",
			length, b.header.Metadata.BlockSize)
	}

	mappedOffset, _, buildID, err := b.header.GetShiftedMapping(off)
	if err != nil {
		return nil, fmt.Errorf("failed to get mapping: %w", err)
	}

	// Pass empty huge page when the build id is nil.
	if *buildID == uuid.Nil {
		return header.EmptyHugePage, nil
	}

	build, err := b.getBuild(buildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get build: %w", err)
	}

	return build.Slice(mappedOffset, int64(b.header.Metadata.BlockSize))
}

func (b *File) getBuild(buildID *uuid.UUID) (Diff, error) {
	storageDiff := newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		int64(b.header.Metadata.BlockSize),
		b.metrics,
		b.persistence,
	)

	source, err := b.store.Get(storageDiff)
	if err != nil {
		return nil, fmt.Errorf("failed to get build from store: %w", err)
	}

	return source, nil
}
