package build

import (
	"fmt"
	"io"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type File struct {
	header      *header.Header
	store       *DiffStore
	fileType    DiffType
	persistence storage.StorageProvider
}

func NewFile(
	header *header.Header,
	store *DiffStore,
	fileType DiffType,
	persistence storage.StorageProvider,
) *File {
	return &File{
		header:      header,
		store:       store,
		fileType:    fileType,
		persistence: persistence,
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

		readLength := min(mappedLength, remainingReadLength)

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

		buildN, err := mappedBuild.ReadAt(
			p[n:int64(n)+readLength],
			mappedOffset,
		)
		if err != nil {
			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += buildN
	}

	return n, nil
}

// The slice access must be in the predefined blocksize of the build.
func (b *File) Slice(off, length int64) ([]byte, error) {
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
		b.persistence,
	)

	source, err := b.store.Get(storageDiff)
	if err != nil {
		return nil, fmt.Errorf("failed to get build from store: %w", err)
	}

	return source, nil
}
