package build

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/google/uuid"
	"go.uber.org/zap"

	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type File struct {
	Header *header.Header

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
		Header:      header,
		store:       store,
		fileType:    fileType,
		persistence: persistence,
		metrics:     metrics,
	}
}

func (b *File) FinalHeader(ctx context.Context) (*header.Header, error) {
	p2p, ok := b.persistence.(storage.P2PProvider)
	if !ok {
		if err := b.Header.WaitUntilFinal(ctx); err != nil {
			return nil, fmt.Errorf("wait local deps: %w", err)
		}

		return b.Header, nil
	}

	// P2P-backed: ask the peer for its final state. The peer's handler waits
	// on its own self (which transitively covers its chain) before responding.
	// On error / not_available, fall back to the object store — a locally-
	// cached (wire-born) Header may be stale and would silently produce a
	// corrupt child header. Both fail → propagate; uploader retries.
	buildID := b.Header.Metadata.BuildId.String()
	memBytes, rootBytes, peerErr := p2p.WaitForPeerAvailability(ctx, buildID)
	if peerErr == nil {
		// Peer may respond OK but with empty bytes for our file type
		// (e.g., the peer doesn't have this side of the snapshot yet).
		// Fall through to the object store instead of erroring.
		if picked := b.pickByFileType(memBytes, rootBytes); len(picked) > 0 {
			return b.swapHeader(picked)
		}
	}

	logger.L().Warn(ctx, "peer rpc failed, try remote storage",
		zap.String("build_id", buildID),
		zap.String("file_type", string(b.fileType)),
		zap.Error(peerErr),
	)
	data, gcsErr := storage.LoadBlob(ctx, b.persistence, headerPathFor(buildID, b.fileType), storage.MetadataObjectType)
	if gcsErr != nil {
		return nil, fmt.Errorf("peer rpc and object store both failed: peer=%w: object store: %w", peerErr, gcsErr)
	}

	return b.swapHeader(data)
}

func (b *File) pickByFileType(memBytes, rootfsBytes []byte) []byte {
	if b.fileType == Rootfs {
		return rootfsBytes
	}

	return memBytes
}

func headerPathFor(buildID string, ft DiffType) string {
	paths := storage.Paths{BuildID: buildID}
	if ft == Rootfs {
		return paths.RootfsHeader()
	}

	return paths.MemfileHeader()
}

// maxTransitionRetries caps the number of header-swap retries when the peer
// signals upload completion via PeerTransitionedError. After a successful CAS,
// subsequent swapHeader calls are no-ops, so without a limit the loop would
// retry the same failing read forever.
const maxTransitionRetries = 2

func (b *File) ReadAt(ctx context.Context, p []byte, off int64) (n int, err error) {
	transitionRetries := 0

	for n < len(p) {
		h := b.Header

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

		dep := h.LookupDependency(mappedToBuild.BuildId)
		mappedBuild, err := b.getBuild(ctx, mappedToBuild.BuildId, dep.Size, dep.FrameTable.CompressionType())
		if err != nil {
			return 0, fmt.Errorf("failed to get build: %w", err)
		}

		buildN, err := mappedBuild.ReadAt(ctx,
			p[n:int64(n)+readLength],
			int64(mappedToBuild.Offset),
			dep.FrameTable,
		)
		if err != nil {
			if retry, swapErr := b.retryOnTransition(ctx, err, &transitionRetries); retry {
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
	transitionRetries := 0

	for {
		h := b.Header

		mappedBuild, err := h.GetShiftedMapping(ctx, off)
		if err != nil {
			return nil, fmt.Errorf("failed to get mapping: %w", err)
		}

		// Pass empty huge page when the build id is nil.
		if mappedBuild.BuildId == uuid.Nil {
			return header.EmptyHugePage, nil
		}

		dep := h.LookupDependency(mappedBuild.BuildId)
		diff, err := b.getBuild(ctx, mappedBuild.BuildId, dep.Size, dep.FrameTable.CompressionType())
		if err != nil {
			return nil, fmt.Errorf("failed to get build: %w", err)
		}

		result, err := diff.Slice(ctx, int64(mappedBuild.Offset), int64(h.Metadata.BlockSize), dep.FrameTable)
		if err != nil {
			if retry, swapErr := b.retryOnTransition(ctx, err, &transitionRetries); retry {
				continue
			} else if swapErr != nil {
				return nil, swapErr
			}

			return nil, err
		}

		return result, nil
	}
}

// retryOnTransition checks if err is a PeerTransitionedError and swaps the
// header if the retry budget allows. Returns (true, nil) to signal the caller
// should continue the loop, or (false, swapErr) if the swap itself failed
func (b *File) retryOnTransition(ctx context.Context, err error, retries *int) (retry bool, swapErr error) {
	var transErr *storage.PeerTransitionedError
	if !errors.As(err, &transErr) || *retries >= maxTransitionRetries {
		return false, nil
	}

	*retries++

	logger.L().Info(ctx, "peer transition detected, swapping header",
		zap.String("file_type", string(b.fileType)),
		zap.Int("retry", *retries),
	)

	if _, err := b.swapHeader(b.pickByFileType(transErr.MemfileHeader, transErr.RootfsHeader)); err != nil {
		return false, fmt.Errorf("failed to swap header: %w", err)
	}

	return true, nil
}

// swapHeader deserializes header bytes and adopts the result into
// b.Header.
func (b *File) swapHeader(bytes []byte) (*header.Header, error) {
	if len(bytes) == 0 {
		return nil, errors.New("no header bytes available")
	}
	finalH, err := header.DeserializeBytes(bytes)
	if err != nil {
		return nil, fmt.Errorf("deserialize header: %w", err)
	}

	return b.Header.Swap(finalH), nil
}

func (b *File) getBuild(ctx context.Context, buildID uuid.UUID, uncompressedSize int64, ct storage.CompressionType) (Diff, error) {
	storageDiff, err := newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		int64(b.Header.Metadata.BlockSize),
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
