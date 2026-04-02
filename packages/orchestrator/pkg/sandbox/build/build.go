package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/google/uuid"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type File struct {
	header      atomic.Pointer[header.Header]
	swapFailed  atomic.Bool // set if header deserialization fails during P2P transition
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
	f := &File{
		store:       store,
		fileType:    fileType,
		persistence: persistence,
		metrics:     metrics,
	}
	f.header.Store(header)

	return f
}

// Header returns the current header. After a peer transition the header may
// have been atomically swapped to a V4 header containing FrameTables.
func (b *File) Header() *header.Header {
	return b.header.Load()
}

func (b *File) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	for n < len(p) {
		h := b.header.Load()

		mappedToBuild, err := h.GetShiftedMapping(ctx, off+int64(n))
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

		size := b.buildFileSize(h, mappedToBuild.BuildId)
		mappedBuild, err := b.getBuild(ctx, mappedToBuild.BuildId, size, mappedToBuild.FrameTable.CompressionType())
		if err != nil {
			return 0, fmt.Errorf("failed to get build: %w", err)
		}

		buildN, err := mappedBuild.ReadAt(ctx,
			p[n:int64(n)+readLength],
			int64(mappedToBuild.Offset),
			mappedToBuild.FrameTable,
		)
		if err != nil {
			var transErr *storage.PeerTransitionedError
			if errors.As(err, &transErr) && !b.swapFailed.Load() {
				if swapErr := b.swapHeader(transErr); swapErr != nil {
					return 0, fmt.Errorf("failed to swap header: %w", swapErr)
				}

				continue // retry with the new header
			}

			return 0, fmt.Errorf("failed to read from source: %w", err)
		}

		n += buildN
	}

	return n, nil
}

// The slice access must be in the predefined blocksize of the build.
func (b *File) Slice(ctx context.Context, off, _ int64) ([]byte, error) {
	for {
		h := b.header.Load()

		mappedBuild, err := h.GetShiftedMapping(ctx, off)
		if err != nil {
			return nil, fmt.Errorf("failed to get mapping: %w", err)
		}

		// Pass empty huge page when the build id is nil.
		if mappedBuild.BuildId == uuid.Nil {
			return header.EmptyHugePage, nil
		}

		size := b.buildFileSize(h, mappedBuild.BuildId)
		diff, err := b.getBuild(ctx, mappedBuild.BuildId, size, mappedBuild.FrameTable.CompressionType())
		if err != nil {
			return nil, fmt.Errorf("failed to get build: %w", err)
		}

		result, err := diff.Slice(ctx, int64(mappedBuild.Offset), int64(h.Metadata.BlockSize), mappedBuild.FrameTable)
		if err != nil {
			var transErr *storage.PeerTransitionedError
			if errors.As(err, &transErr) && !b.swapFailed.Load() {
				if swapErr := b.swapHeader(transErr); swapErr != nil {
					return nil, fmt.Errorf("failed to swap header: %w", swapErr)
				}

				continue // retry with the new header
			}

			return nil, err
		}

		return result, nil
	}
}

// swapHeader atomically replaces the header when the peer signals upload
// completion. Only the first goroutine to CAS succeeds; others just retry
// with the already-swapped header. On deserialization failure, marks the
// swap as failed so the ReadAt/Slice loop doesn't retry indefinitely.
func (b *File) swapHeader(transErr *storage.PeerTransitionedError) error {
	var headerBytes []byte

	switch b.fileType {
	case Memfile:
		headerBytes = transErr.MemfileHeader
	case Rootfs:
		headerBytes = transErr.RootfsHeader
	}

	if len(headerBytes) == 0 {
		return fmt.Errorf("no header bytes available")
	}

	newH, err := header.DeserializeBytes(headerBytes)
	if err != nil {
		b.swapFailed.Store(true)

		return fmt.Errorf("failed to swap header: %w", err)
	}

	old := b.header.Load()
	b.header.CompareAndSwap(old, newH)

	return nil
}

// buildFileSize returns the uncompressed file size for buildID from the
// header's BuildFiles map. Returns 0 for V3 headers (no BuildFiles), which
// signals the read path to fall back to a Size() RPC.
func (b *File) buildFileSize(h *header.Header, buildID uuid.UUID) int64 {
	if info, ok := h.BuildFiles[buildID]; ok {
		return info.Size
	}

	return 0
}

func (b *File) getBuild(ctx context.Context, buildID uuid.UUID, uncompressedSize int64, ct storage.CompressionType) (Diff, error) {
	storageDiff, err := newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		int64(b.Header().Metadata.BlockSize),
		b.metrics,
		b.persistence,
		uncompressedSize, ct,
		b.store.flags,
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
