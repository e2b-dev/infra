//go:build linux

package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
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

// ReadAt fills p from the mapped build segments, optionally in parallel.
// Cache eviction or a peer transition re-resolves and retries.
func (b *File) ReadAt(ctx context.Context, p []byte, off int64) (int, error) {
	maxParallel := b.store.flags.IntFlag(ctx, featureflags.MaxParallelBuildReadSegments)

	for {
		segments, n, err := b.planRead(ctx, p, off)
		if err == nil {
			err = b.readSegments(ctx, p, segments, maxParallel)
		}
		if err == nil {
			// Fewer bytes than requested means the mappings ran out: report the
			// bytes filled so far with io.EOF, matching io.ReaderAt semantics.
			if n < len(p) {
				return n, io.EOF
			}

			return n, nil
		}

		// A Diff can be evicted and closed between planning and reading. Re-plan
		// the whole read; reads are idempotent, so re-filling already-written
		// regions is safe and getBuild re-resolves the closed Diff.
		var closed *block.CacheClosedError
		if errors.As(err, &closed) {
			continue
		}

		return 0, err
	}
}

type readSegment struct {
	dstOff int
	srcOff int64
	length int64
	diff   Diff
	// ft uses the nil-vs-empty convention: nil = no entry (hint unknown),
	// empty &FrameTable{} = authoritatively uncompressed, non-empty = compressed.
	ft *storage.FrameTable
}

func (b *File) readSegments(ctx context.Context, p []byte, segments []readSegment, maxParallel int) error {
	if maxParallel > 1 && len(segments) > 1 {
		g, gctx := errgroup.WithContext(ctx)
		g.SetLimit(maxParallel)
		for _, s := range segments {
			seg := s
			g.Go(func() error { return b.readSegment(gctx, p, seg) })
		}

		return g.Wait()
	}

	for _, s := range segments {
		if err := b.readSegment(ctx, p, s); err != nil {
			return err
		}
	}

	return nil
}

// readSegment reads one segment, recovering from PeerTransitionedError or a
// wrong-CT 404 by refreshing the FT from storage and retrying once.
func (b *File) readSegment(ctx context.Context, p []byte, s readSegment) error {
	refresher, _ := s.diff.(FrameTableRefresher)
	dst := p[s.dstOff : s.dstOff+int(s.length)]

	n, err := s.diff.ReadAt(ctx, dst, s.srcOff, s.ft)
	if err != nil {
		if refresher == nil {
			return err
		}
		if err = b.specialErrorHandler(ctx, refresher, s, err); err != nil {
			return err
		}
		n, err = s.diff.ReadAt(ctx, dst, s.srcOff, s.ft)
		if err != nil {
			return err
		}
	}
	if int64(n) != s.length {
		return io.ErrUnexpectedEOF
	}

	return nil
}

func (b *File) specialErrorHandler(ctx context.Context, refresher FrameTableRefresher, s readSegment, err error) error {
	var transitionErr *storage.PeerTransitionedError
	switch {
	case errors.As(err, &transitionErr):
		if waitErr := waitTransitionBackoff(ctx, b.fileType, transitionErr); waitErr != nil {
			return waitErr
		}
	case errors.Is(err, storage.ErrObjectNotExist) && s.ft == nil:
		// fall through to refresh
	default:
		return err
	}

	h, rerr := refresher.RefreshFrameTable(ctx)
	if rerr != nil {
		return fmt.Errorf("recover after %w: %w", err, rerr)
	}
	// If the just-loaded header is *this* file's own finalized header, promote
	// it: every subsequent read picks up authoritative Builds[ancestor] hints
	// instead of paying a separate refresh per ancestor.
	if h != nil && h.Metadata.BuildId == b.Header().Metadata.BuildId {
		b.SwapHeader(h)
	}

	return nil
}

// planRead resolves the segments covering p, zero-filling uuid.Nil regions.
// A returned byte count below len(p) means the mappings ran out (EOF).
func (b *File) planRead(ctx context.Context, p []byte, off int64) ([]readSegment, int, error) {
	// Per-read Diff cache: avoids the DiffStore TTL cache mutex on every mapping.
	const buildCacheSize = 16
	var (
		underlyingIDs   [buildCacheSize]uuid.UUID
		underlyingDiffs [buildCacheSize]Diff
		cacheIDs        = underlyingIDs[:0]
		cacheDiffs      = underlyingDiffs[:0]
		segments        []readSegment
	)

	var n int
	for n < len(p) {
		h := b.Header()
		mappedToBuild, err := h.GetShiftedMapping(ctx, off+int64(n))
		if err != nil {
			return nil, 0, fmt.Errorf("failed to get mapping: %w", err)
		}
		readLength := min(int64(mappedToBuild.Length), int64(len(p)-n))
		// A zero-length mapping means off+n is past the last mapping (EOF); stop
		// and let the caller surface io.EOF for the bytes covered so far.
		if readLength <= 0 {
			return segments, n, nil
		}
		// uuid.Nil marks an unmapped/empty region; zero-fill it in place.
		if mappedToBuild.BuildId == uuid.Nil {
			clear(p[n : n+int(readLength)])
			n += int(readLength)

			continue
		}

		diff, err := b.cachedBuild(ctx, h, mappedToBuild.BuildId, &cacheIDs, &cacheDiffs)
		if err != nil {
			return nil, 0, err
		}
		segments = append(segments, readSegment{
			dstOff: n,
			srcOff: int64(mappedToBuild.Offset),
			length: readLength,
			diff:   diff,
			ft:     h.GetBuildFrameData(mappedToBuild.BuildId),
		})
		n += int(readLength)
	}

	return segments, n, nil
}

func (b *File) cachedBuild(ctx context.Context, h *header.Header, buildID uuid.UUID, ids *[]uuid.UUID, diffs *[]Diff) (Diff, error) {
	for i, id := range *ids {
		if id == buildID {
			return (*diffs)[i], nil
		}
	}

	diff, err := b.getBuild(ctx, buildID, b.buildFileSize(h, buildID), h.GetBuildFrameData(buildID).CompressionType())
	if err != nil {
		return nil, fmt.Errorf("failed to get build: %w", err)
	}
	if len(*ids) < cap(*ids) {
		*ids = append(*ids, buildID)
		*diffs = append(*diffs, diff)
	}

	return diff, nil
}

// Slice returns [off, off+length). Zero-copy when the range fits in a
// single mapping; otherwise composes via ReadAt.
func (b *File) Slice(ctx context.Context, off, length int64) ([]byte, error) {
	if length > 0 {
		h := b.Header()
		m, err := h.GetShiftedMapping(ctx, off)
		if err == nil && int64(m.Length) >= length {
			if m.BuildId == uuid.Nil && length <= int64(len(header.EmptyHugePage)) {
				return header.EmptyHugePage[:length], nil
			}
			if m.BuildId != uuid.Nil {
				size := b.buildFileSize(h, m.BuildId)
				ft := h.GetBuildFrameData(m.BuildId)
				diff, derr := b.getBuild(ctx, m.BuildId, size, ft.CompressionType())
				if derr != nil {
					logger.L().Warn(ctx, "failed to get build for slice fast path", zap.Error(derr))
				} else {
					slice, sErr := diff.Slice(ctx, int64(m.Offset), length, ft)
					if sErr == nil {
						return slice, nil
					}
					logger.L().Warn(ctx, "failed to slice build fast path", zap.Error(sErr))
				}
				// Errors fall through to ReadAt.
			}
		}
	}
	out := make([]byte, length)
	if _, err := b.ReadAt(ctx, out, off); err != nil {
		return nil, fmt.Errorf("failed to read at: %w", err)
	}

	return out, nil
}

// IsCached reports whether the range is fully resident locally; uuid.Nil
// counts as cached, uninitialized StorageDiffs as uncached. No I/O.
func (b *File) IsCached(ctx context.Context, off, length int64) bool {
	h := b.Header()
	if h == nil {
		return false
	}

	var n int64
	for n < length {
		m, err := h.GetShiftedMapping(ctx, off+n)
		if err != nil {
			return false
		}
		segLen := min(int64(m.Length), length-n)
		if segLen <= 0 {
			return false
		}

		if m.BuildId != uuid.Nil {
			diff, ok := b.store.Lookup(GetDiffStoreKey(m.BuildId.String(), b.fileType))
			if !ok {
				return false
			}
			peeker, ok := diff.(block.CachePeeker)
			if !ok || !peeker.IsCached(ctx, int64(m.Offset), segLen) {
				return false
			}
		}

		n += segLen
	}

	return true
}

// waitTransitionBackoff honors the peer's RetryAfter hint before the caller
// retries against base storage. Returns ctx.Err() if cancelled during sleep.
func waitTransitionBackoff(ctx context.Context, fileType DiffType, transErr *storage.PeerTransitionedError) error {
	if transErr.RetryAfter > 0 {
		timer := time.NewTimer(transErr.RetryAfter)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	logger.L().Info(ctx, "peer transition detected, recovering", zap.String("file_type", string(fileType)))

	return nil
}

// buildFileSize returns the uncompressed file size for a build. Returns 0 for
// V3 headers, which signals the read path to fall back to a Size() RPC.
func (b *File) buildFileSize(h *header.Header, buildID uuid.UUID) int64 {
	if bd, ok := h.Builds[buildID]; ok {
		return bd.Size
	}

	return 0
}

func (b *File) getBuild(ctx context.Context, buildID uuid.UUID, uncompressedSize int64, hintCT storage.CompressionType) (Diff, error) {
	storageDiff, err := newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		int64(b.Header().Metadata.BlockSize),
		b.metrics,
		b.persistence,
		uncompressedSize, hintCT,
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
