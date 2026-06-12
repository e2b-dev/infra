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
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

var (
	buildMeter = otel.Meter("github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build")

	// frameTableRefreshTimer measures ancestor-header refresh events as the
	// standard (duration, bytes, count) triple. A refresh is triggered when a
	// parent had no Builds[self] entry (proactive load at source resolution)
	// or a P2P peer transitions to storage mid-read.
	//
	// Attributes:
	//   cause     — proactive | peer_transitioned
	//   file_type — memfile | rootfs
	//   result    — success | failure (added by TimerFactory)
	frameTableRefreshTimer = utils.Must(telemetry.NewTimerFactory(buildMeter,
		"orchestrator.storage.diff.frame_table_refresh",
		"Duration of frame-table refresh header loads",
		"Bytes loaded during frame-table refreshes",
		"Frame-table refresh events",
	))
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
		segments, n, distinctBuilds, err := b.planRead(ctx, p, off)
		if err == nil {
			err = b.readSegments(ctx, p, segments, maxParallel)
		}
		if err == nil {
			// Recorded only for the attempt that succeeded, so eviction /
			// transition retries don't double-count the read.
			recordReadFanout(ctx, b.fileType, len(segments), distinctBuilds)

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

	// ft uses the nil-vs-empty convention: nil = no entry,
	// storage.UncompressedFrameTable = authoritatively uncompressed,
	// non-empty = see ft.compressionType.
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

// readSegment reads one segment. On PeerTransitionedError, it waits the
// peer's RetryAfter, refreshes the diff's source against the post-finalize
// header/CT, and retries once. All other errors propagate.
func (b *File) readSegment(ctx context.Context, p []byte, s readSegment) error {
	dst := p[s.dstOff : s.dstOff+int(s.length)]

	n, err := s.diff.ReadAt(ctx, dst, s.srcOff, s.ft)
	if err != nil {
		var transitionErr *storage.PeerTransitionedError
		if !errors.As(err, &transitionErr) {
			return err
		}
		if err = waitTransitionBackoff(ctx, transitionErr); err != nil {
			return err
		}
		if refreshErr := s.diff.RefreshSource(ctx); refreshErr != nil {
			return fmt.Errorf("refresh after peer transition: %w", refreshErr)
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

// planRead resolves the segments covering p, zero-filling uuid.Nil regions.
// A returned byte count below len(p) means the mappings ran out (EOF).
// distinctBuilds counts the distinct builds the segments reference; it
// saturates at buildCacheSize when a single read crosses more builds than the
// per-read cache holds (already deep in the "very fragmented" regime).
func (b *File) planRead(ctx context.Context, p []byte, off int64) (segments []readSegment, n int, distinctBuilds int, err error) {
	// Per-read Diff cache: avoids the DiffStore TTL cache mutex on every mapping.
	const buildCacheSize = 16
	var (
		underlyingIDs   [buildCacheSize]uuid.UUID
		underlyingDiffs [buildCacheSize]Diff
		cacheIDs        = underlyingIDs[:0]
		cacheDiffs      = underlyingDiffs[:0]
	)

	for n < len(p) {
		h := b.Header()
		mappedToBuild, err := h.GetShiftedMapping(ctx, off+int64(n))
		if err != nil {
			return nil, 0, 0, fmt.Errorf("failed to get mapping: %w", err)
		}
		readLength := min(int64(mappedToBuild.Length), int64(len(p)-n))
		// A zero-length mapping means off+n is past the last mapping (EOF); stop
		// and let the caller surface io.EOF for the bytes covered so far.
		if readLength <= 0 {
			return segments, n, len(cacheIDs), nil
		}
		// uuid.Nil marks an unmapped/empty region; zero-fill it in place.
		if mappedToBuild.BuildId == uuid.Nil {
			clear(p[n : n+int(readLength)])
			n += int(readLength)

			continue
		}

		diff, err := b.cachedBuild(ctx, mappedToBuild.BuildId, &cacheIDs, &cacheDiffs)
		if err != nil {
			return nil, 0, 0, err
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

	return segments, n, len(cacheIDs), nil
}

func (b *File) cachedBuild(ctx context.Context, buildID uuid.UUID, ids *[]uuid.UUID, diffs *[]Diff) (Diff, error) {
	for i, id := range *ids {
		if id == buildID {
			return (*diffs)[i], nil
		}
	}

	diff, err := b.getBuild(ctx, buildID)
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
				ft := h.GetBuildFrameData(m.BuildId)
				diff, derr := b.getBuild(ctx, m.BuildId)
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
func waitTransitionBackoff(ctx context.Context, transErr *storage.PeerTransitionedError) error {
	if transErr.RetryAfter > 0 {
		timer := time.NewTimer(transErr.RetryAfter)
		defer timer.Stop()
		select {
		case <-timer.C:
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

// getBuild returns the cached StorageDiff for buildID, constructing one via
// createDiff on miss. The singleflight inside GetOrCreate ensures createDiff
// runs at most once per key across concurrent callers.
func (b *File) getBuild(ctx context.Context, buildID uuid.UUID) (Diff, error) {
	key := GetDiffStoreKey(buildID.String(), b.fileType)

	return b.store.GetOrCreate(ctx, key, func(ctx context.Context) (Diff, error) {
		return b.createDiff(ctx, buildID)
	})
}

func (b *File) createDiff(ctx context.Context, buildID uuid.UUID) (Diff, error) {
	h := b.Header()
	blockSize := int64(h.Metadata.BlockSize)

	objType, ok := storageObjectType(b.fileType)
	if !ok {
		return nil, UnknownDiffTypeError{b.fileType}
	}

	var (
		upstream  storage.Seekable
		size      int64
		initialCT storage.CompressionType
		initialFT *storage.FullFrameTable
	)

	bd, hasEntry := h.Builds[buildID]
	switch {
	case hasEntry:
		// Our header has a Builds entry for this ancestor. Do NOT latch
		// bd.FrameData as the StorageDiff's authoritative full-file FT — it is
		// filtered to only the frames our header references, not the ancestor's
		// full table.
		size = bd.Size
		initialCT = bd.FrameData.CompressionType()

	case h.Metadata.Version >= header.MetadataVersionV4:
		peerActive := b.store.isActivePeer != nil && b.store.isActivePeer(buildID.String())
		if peerActive {
			// Peer mode is active for the build. Open at the uncompressed path
			// (peers serve uncompressed by basic name regardless of stored CT)
			// and ask the peer for size. initFT stays nil (as opposed to {})
			// since we do not know what it is.
			var err error
			upstream, err = b.openUpstream(ctx, buildID, objType, initialCT)
			if err != nil {
				return nil, err
			}
			if peerReportedSize, ok, err := initialSize(ctx, upstream); err != nil {
				return nil, fmt.Errorf("createDiff: peer Size for build %s: %w", buildID, err)
			} else if ok {
				size = peerReportedSize

				break
			}

			// fall through to refresh.
		}

		// Refresh ancestor and open upstream.
		var err error
		upstream, size, initialFT, err = b.refreshAncestorAndOpenUpstream(ctx, buildID, objType)
		if err != nil {
			return nil, err
		}

	default:
		initialFT = storage.UncompressedFullFrameTable
	}

	if upstream == nil {
		var err error
		upstream, err = b.openUpstream(ctx, buildID, objType, initialCT)
		if err != nil {
			return nil, err
		}
	}

	if size == 0 {
		// (d) and degenerate (a) where bd.Size was zero. Ask storage directly.
		var err error
		size, err = upstream.Size(ctx)
		if err != nil {
			return nil, fmt.Errorf("createDiff: size lookup for build %s: %w", buildID, err)
		}
	}

	return newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		objType,
		blockSize,
		b.metrics,
		b.persistence,
		b.store.isActivePeer,
		upstream,
		size,
		initialFT,
		b.store.flags,
	)
}

// openUpstream resolves the data-file path for buildID at ct and opens it.
func (b *File) openUpstream(ctx context.Context, buildID uuid.UUID, objType storage.SeekableObjectType, ct storage.CompressionType) (storage.Seekable, error) {
	path := storage.Paths{BuildID: buildID.String()}.DataFile(string(b.fileType), ct)
	upstream, err := b.persistence.OpenSeekable(ctx, path, objType)
	if err != nil {
		return nil, fmt.Errorf("createDiff: open upstream for build %s at %s: %w", buildID, path, err)
	}

	return upstream, nil
}

func (b *File) refreshAncestorAndOpenUpstream(ctx context.Context, buildID uuid.UUID, objType storage.SeekableObjectType) (storage.Seekable, int64, *storage.FullFrameTable, error) {
	loaded, err := refreshBuildHeader(ctx, b.persistence, buildID, b.fileType, refreshCauseProactive)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("createDiff: proactive header load for build %s: %w", buildID, err)
	}

	// Promote a self-matching loaded header if authoritative.
	if h := b.Header(); loaded.Metadata.BuildId == h.Metadata.BuildId {
		if _, hasSelf := loaded.Builds[loaded.Metadata.BuildId]; hasSelf {
			b.SwapHeader(loaded)
		}
	}

	// Pre-V4 ancestor headers (old template builds) carry no Builds map at
	// all: their data file is stored uncompressed at the basic path. Latch
	// that authoritatively — failing the self-entry lookup here breaks every
	// V4 snapshot that still maps pages to a V3-era ancestor (sandbox resume
	// then EIOs on first uncached read).
	if loaded.Metadata.Version < header.MetadataVersionV4 {
		upstream, err := b.openUpstream(ctx, buildID, objType, storage.CompressionNone)
		if err != nil {
			return nil, 0, nil, err
		}

		// Size 0: createDiff falls back to upstream.Size, matching the
		// pre-refresh behavior for V3 builds.
		return upstream, 0, storage.UncompressedFullFrameTable, nil
	}

	// A finalized V4+ storage header always carries a self entry
	// (build_upload_v4 populates it before publish). A missing self entry here
	// means a routed OpenBlob hit a peer's in-flight header — which shouldn't
	// be possible on this code path (we entered after !peerActive). Surface
	// loudly rather than silently latching a zero-value bd as an authoritative
	// uncompressed FT.
	size, ft, err := loaded.SelfBuildData()
	if err != nil {
		return nil, 0, nil, fmt.Errorf("createDiff: %w", err)
	}

	upstream, err := b.openUpstream(ctx, buildID, objType, ft.Table().CompressionType())
	if err != nil {
		return nil, 0, nil, err
	}

	return upstream, size, ft, nil
}

// initialSize is THE only production code path that calls Size on
// a freshly opened upstream. Invoked from createDiff when the V4+ ancestor is
// peer-active: ask the peer wrapper for the size. Four outcomes:
//
//   - peer-routed wrapper, peer answered → (size, true, nil)
//   - peer-routed wrapper, PeerTransitionedError → (0, false, nil)  caller refreshes
//   - peer-routed wrapper, peer RPC failure → (0, false, err)
//   - NOT peer-routed (resolveProvider cleared between IsActive probe and
//     OpenSeekable) → (0, false, nil)  caller refreshes
//
// Symmetric with readSegment's PeerTransitionedError handling on the read path:
// peer says "go to storage" → refresh authoritative header → continue.
// No 404-driven recovery.
func initialSize(ctx context.Context, upstream storage.Seekable) (size int64, ok bool, err error) {
	if _, peerRouted := upstream.(peerclient.PeerRouted); !peerRouted {
		return 0, false, nil
	}
	size, err = upstream.Size(ctx)
	if err == nil {
		return size, true, nil
	}
	var transErr *storage.PeerTransitionedError
	if errors.As(err, &transErr) {
		return 0, false, nil
	}

	return 0, false, err
}
