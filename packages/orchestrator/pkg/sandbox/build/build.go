package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"

	"github.com/google/uuid"
	"go.uber.org/zap"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type File struct {
	header      atomic.Pointer[header.Header]
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

func (b *File) Header() *header.Header {
	return b.header.Load()
}

func (b *File) SwapHeader(h *header.Header) {
	b.header.Store(h)
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
		ft := h.GetBuildFrameData(mappedToBuild.BuildId)
		mappedBuild, err := b.getBuild(ctx, mappedToBuild.BuildId, size, ft.CompressionType())
		if err != nil {
			return 0, fmt.Errorf("failed to get build: %w", err)
		}

		buildN, err := mappedBuild.ReadAt(ctx,
			p[n:int64(n)+readLength],
			int64(mappedToBuild.Offset),
			ft,
		)
		if err != nil {
			if retry, swapErr := b.retryOnTransition(ctx, err); retry {
				continue
			} else if swapErr != nil {
				return 0, swapErr
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
		ft := h.GetBuildFrameData(mappedBuild.BuildId)
		diff, err := b.getBuild(ctx, mappedBuild.BuildId, size, ft.CompressionType())
		if err != nil {
			return nil, fmt.Errorf("failed to get build: %w", err)
		}

		result, err := diff.Slice(ctx, int64(mappedBuild.Offset), int64(h.Metadata.BlockSize), ft)
		if err != nil {
			if retry, swapErr := b.retryOnTransition(ctx, err); retry {
				continue
			} else if swapErr != nil {
				return nil, swapErr
			}

			return nil, err
		}

		return result, nil
	}
}

// retryOnTransition catches a PeerTransitionedError and swaps the header from
// storage. Returns (true, nil) to signal the caller should continue the loop,
// or (false, swapErr) if the swap itself failed. peerSeekable emits the
// transition error at most once per seekable, so the loop is naturally
// bounded — no retry counter needed here.
//
// The transition is signaled only after the source upload has finalized, so
// the header object already exists in storage. A single LoadHeader is enough;
// polling here would multiply GCS reads under high peer-transition rates.
func (b *File) retryOnTransition(ctx context.Context, err error) (bool, error) {
	var transErr *storage.PeerTransitionedError
	if !errors.As(err, &transErr) {
		return false, nil
	}

	logger.L().Info(ctx, "peer transition detected, swapping header",
		zap.String("file_type", string(b.fileType)),
	)

	hdrPath := storage.Paths{BuildID: b.header.Load().Metadata.BuildId.String()}.HeaderFile(string(b.fileType))
	h, loadErr := header.LoadHeader(ctx, b.persistence, hdrPath)
	if loadErr != nil {
		return false, fmt.Errorf("failed to swap header: %w", loadErr)
	}
	b.SwapHeader(h)

	return true, nil
}

// buildFileSize returns the uncompressed file size for a build. Returns 0 for
// V3 headers, which signals the read path to fall back to a Size() RPC.
func (b *File) buildFileSize(h *header.Header, buildID uuid.UUID) int64 {
	if bd, ok := h.Builds[buildID]; ok {
		return bd.Size
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
