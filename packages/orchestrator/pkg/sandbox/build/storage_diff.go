//go:build linux

package build

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template/peerclient"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	refreshCauseProactive        = "proactive"
	refreshCausePeerTransitioned = "peer_transitioned"
)

// source carries the StorageDiff's current routing state. upstream is always
// non-nil after construction but may be switched once over the lifetime; ft
// nil = not authoritative, non-nil = authoritative and immutable.
// ft is *FullFrameTable rather than *FrameTable — see
// (*header.Header).SelfBuildData for the invariant justifying the upcast.
type source struct {
	upstream           storage.RangeOpener
	fullDiffFrameTable *storage.FullFrameTable
}

func isPeerRouted(v any) bool {
	_, ok := v.(peerclient.PeerRouted)

	return ok
}

type StorageDiff struct {
	chunker           *block.Chunker
	cachePath         string
	cacheKey          DiffStoreKey
	buildID           string
	diffType          DiffType
	storageObjectType storage.SeekableObjectType

	blockSize   int64
	metrics     blockmetrics.Metrics
	persistence storage.StorageProvider

	source    atomic.Pointer[source]
	refreshMu sync.Mutex
}

var _ Diff = (*StorageDiff)(nil)

type UnknownDiffTypeError struct {
	DiffType DiffType
}

func (e UnknownDiffTypeError) Error() string {
	return fmt.Sprintf("unknown diff type: %s", e.DiffType)
}

// newStorageDiff assembles a StorageDiff from a fully-resolved upstream, size,
// and full-file FrameTable. All regime decisioning (peer-active bootstrap, V3
// fallback, authoritative-refresh recovery) lives in the caller (createDiff);
// this constructor is intentionally pure.
func newStorageDiff(
	basePath string,
	buildID string,
	diffType DiffType,
	storageObjectType storage.SeekableObjectType,
	blockSize int64,
	metrics blockmetrics.Metrics,
	persistence storage.StorageProvider,
	upstream storage.Seekable,
	uncompressedSize int64,
	initialFT *storage.FullFrameTable,
	ff *featureflags.Client,
) (*StorageDiff, error) {
	cachePath := GenerateDiffCachePath(basePath, buildID, diffType)
	c, err := block.NewChunker(ff, uncompressedSize, blockSize, cachePath, metrics, storageObjectType)
	if err != nil {
		return nil, fmt.Errorf("create chunker for build %s: %w", buildID, err)
	}

	d := &StorageDiff{
		buildID:           buildID,
		diffType:          diffType,
		storageObjectType: storageObjectType,
		cachePath:         cachePath,
		blockSize:         blockSize,
		metrics:           metrics,
		persistence:       persistence,
		chunker:           c,
		cacheKey:          GetDiffStoreKey(buildID, diffType),
	}
	d.source.Store(&source{upstream: upstream, fullDiffFrameTable: initialFT})

	return d, nil
}

func (b *File) createDiff(ctx context.Context, buildID uuid.UUID) (Diff, error) {
	h := b.Header()
	blockSize := int64(h.Metadata.BlockSize)

	objType, ok := storageObjectType(b.fileType)
	if !ok {
		return nil, UnknownDiffTypeError{b.fileType}
	}

	bd, hasEntry := h.Builds[buildID]

	var (
		upstream  storage.Seekable
		size      int64
		initialFT *storage.FullFrameTable
		err       error
	)
	switch {
	case hasEntry:
		// Open at the entry's CT. Do NOT latch bd.FrameData as the
		// authoritative full-file FT — it carries only the frames our header
		// references, not the ancestor's full table.
		upstream, err = b.openDataFile(ctx, buildID, objType, bd.FrameData.CompressionType())
		if err != nil {
			return nil, err
		}
		size = bd.Size

	default:
		// hasEntry=false implies a peer-served header (LoadHeader backfills
		// sentinels for storage-loaded headers, so storage paths always hit
		// CASE=hasEntry). Probe basic-name to detect peer routing; on
		// miss/transition refresh from storage.
		upstream, err = b.openDataFile(ctx, buildID, objType, storage.CompressionNone)
		if err != nil {
			return nil, err
		}
		if isPeerRouted(upstream) {
			peerSize, peerErr := upstream.Size(ctx)
			if peerErr == nil {
				size = peerSize

				break
			}
			var transErr *storage.PeerTransitionedError
			if !errors.As(peerErr, &transErr) {
				return nil, fmt.Errorf("createDiff: peer Size for build %s: %w", buildID, peerErr)
			}
		}
		loaded, lerr := refreshHeader(ctx, b.persistence, buildID, b.fileType, refreshCauseProactive)
		if lerr != nil {
			return nil, fmt.Errorf("createDiff: proactive header load for build %s: %w", buildID, lerr)
		}
		// Promote loaded header on self-match so future pauses inherit the
		// populated Builds map.
		if loaded.Metadata.BuildId == h.Metadata.BuildId {
			if _, hasSelf := loaded.Builds[loaded.Metadata.BuildId]; hasSelf {
				b.SwapHeader(loaded)
			}
		}
		upstream, size, initialFT, err = openFromLoadedHeader(ctx, b.persistence, loaded, b.fileType, objType)
		if err != nil {
			return nil, err
		}
	}

	if size == 0 {
		size, err = upstream.Size(ctx)
		if err != nil {
			return nil, fmt.Errorf("createDiff: size lookup for build %s: %w", buildID, err)
		}
	}

	if isPeerRouted(upstream) {
		initialFT = nil
	}

	return newStorageDiff(
		b.store.cachePath,
		buildID.String(),
		b.fileType,
		objType,
		blockSize,
		b.metrics,
		b.persistence,
		upstream,
		size,
		initialFT,
		b.store.flags,
	)
}

func (b *File) openDataFile(ctx context.Context, buildID uuid.UUID, objType storage.SeekableObjectType, ct storage.CompressionType) (storage.Seekable, error) {
	path := storage.Paths{BuildID: buildID.String()}.DataFile(string(b.fileType), ct)
	upstream, err := b.persistence.OpenSeekable(ctx, path, objType)
	if err != nil {
		return nil, fmt.Errorf("createDiff: open data file for build %s at %s: %w", buildID, path, err)
	}

	return upstream, nil
}

func openFromLoadedHeader(
	ctx context.Context,
	persistence storage.StorageProvider,
	loaded *header.Header,
	fileType DiffType,
	objType storage.SeekableObjectType,
) (storage.Seekable, int64, *storage.FullFrameTable, error) {
	buildID := loaded.Metadata.BuildId
	paths := storage.Paths{BuildID: buildID.String()}
	if loaded.Metadata.Version < header.MetadataVersionV4 {
		path := paths.DataFile(string(fileType), storage.CompressionNone)
		upstream, err := persistence.OpenSeekable(ctx, path, objType)
		if err != nil {
			return nil, 0, nil, fmt.Errorf("reopen uncompressed upstream for pre-V4 build %s at %s: %w", buildID, path, err)
		}

		return upstream, 0, storage.UncompressedFullFrameTable, nil
	}
	size, ft, err := loaded.SelfBuildData()
	if err != nil {
		return nil, 0, nil, err
	}
	path := paths.DataFile(string(fileType), ft.Table().CompressionType())
	upstream, err := persistence.OpenSeekable(ctx, path, objType)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("reopen upstream for build %s at %s: %w", buildID, path, err)
	}

	return upstream, size, ft, nil
}

func storageObjectType(diffType DiffType) (storage.SeekableObjectType, bool) {
	switch diffType {
	case Memfile:
		return storage.MemfileObjectType, true
	case Rootfs:
		return storage.RootFSObjectType, true
	default:
		return storage.UnknownSeekableObjectType, false
	}
}

func (b *StorageDiff) CacheKey() DiffStoreKey {
	return b.cacheKey
}

func (b *StorageDiff) Close() error {
	return b.chunker.Close()
}

func (b *StorageDiff) ReadAt(ctx context.Context, p []byte, off int64, callerFT *storage.FrameTable) (int, error) {
	up, ft, err := b.resolve(ctx, callerFT)
	if err != nil {
		return 0, err
	}
	n, err := b.chunker.ReadAt(ctx, p, off, up, ft)
	var transErr *storage.PeerTransitionedError
	if !errors.As(err, &transErr) {
		return n, err
	}
	if err := waitTransitionBackoff(ctx, transErr); err != nil {
		return 0, err
	}
	if err := b.reloadAfterPeerTransition(ctx); err != nil {
		return 0, fmt.Errorf("refresh after peer transition: %w", err)
	}
	up, ft, err = b.resolve(ctx, callerFT)
	if err != nil {
		return 0, err
	}

	return b.chunker.ReadAt(ctx, p, off, up, ft)
}

func (b *StorageDiff) Slice(ctx context.Context, off, length int64, callerFT *storage.FrameTable) ([]byte, error) {
	up, ft, err := b.resolve(ctx, callerFT)
	if err != nil {
		return nil, err
	}
	out, err := b.chunker.Slice(ctx, off, length, up, ft)
	var transErr *storage.PeerTransitionedError
	if !errors.As(err, &transErr) {
		return out, err
	}
	if err := waitTransitionBackoff(ctx, transErr); err != nil {
		return nil, err
	}
	if err := b.reloadAfterPeerTransition(ctx); err != nil {
		return nil, fmt.Errorf("refresh after peer transition: %w", err)
	}
	up, ft, err = b.resolve(ctx, callerFT)
	if err != nil {
		return nil, err
	}

	return b.chunker.Slice(ctx, off, length, up, ft)
}

// The local file might not be synced.
func (b *StorageDiff) CachePath(context.Context) (string, error) {
	return b.cachePath, nil
}

func (b *StorageDiff) FileSize(ctx context.Context) (int64, error) {
	return b.chunker.FileSize(ctx)
}

func (b *StorageDiff) Size(_ context.Context) (int64, error) {
	return b.chunker.Size(), nil
}

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

// IsCached reports whether [off, off+length) is in the chunker's local cache.
// Side-effect-free.
func (b *StorageDiff) IsCached(ctx context.Context, off, length int64) bool {
	return b.chunker.IsCached(ctx, off, length)
}

func refreshHeader(ctx context.Context, persistence storage.StorageProvider, buildID uuid.UUID, diffType DiffType, cause string) (*header.Header, error) {
	timer := frameTableRefreshTimer.Begin(
		attribute.String("cause", cause),
		attribute.String("file_type", string(diffType)),
	)

	headerPath := storage.Paths{BuildID: buildID.String()}.HeaderFile(string(diffType))
	h, bytesLoaded, err := header.LoadHeader(ctx, persistence, headerPath)
	if err != nil {
		timer.Failure(ctx, int64(bytesLoaded))

		return nil, fmt.Errorf("load header for build %s: %w", buildID, err)
	}
	timer.Success(ctx, int64(bytesLoaded))

	return h, nil
}

// resolve picks the (upstream, ft) the next read should use, given the
// caller's per-mapping FT hint. The contract: if there is no authoritative FT
// latched AND no peer currently serving this build, we MUST refresh before
// reading. The latched upstream was opened at the bootstrap-guessed CT path
// and may return wrong bytes once the peer is gone; only the authoritative
// header tells us where to read from.
func (b *StorageDiff) resolve(ctx context.Context, callerFT *storage.FrameTable) (storage.RangeOpener, *storage.FrameTable, error) {
	cur := b.source.Load()
	if cur.fullDiffFrameTable != nil {
		return cur.upstream, cur.fullDiffFrameTable.Table(), nil
	}
	if callerFT != nil {
		return cur.upstream, callerFT, nil
	}
	if isPeerRouted(cur.upstream) {
		return cur.upstream, storage.UncompressedFrameTable, nil
	}
	if err := b.reloadProactive(ctx); err != nil {
		return nil, nil, fmt.Errorf("resolve: %w", err)
	}
	cur = b.source.Load()

	return cur.upstream, cur.fullDiffFrameTable.Table(), nil
}

// reloadAfterPeerTransition refreshes the source after a peerSeekable signaled
// PeerTransitionedError. Short-circuits if a concurrent goroutine already
// swapped the upstream to non-peer.
func (b *StorageDiff) reloadAfterPeerTransition(ctx context.Context) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	if !isPeerRouted(b.source.Load().upstream) {
		return nil
	}

	return b.reloadSourceLocked(ctx, refreshCausePeerTransitioned)
}

// reloadProactive refreshes the source when resolve has no authoritative FT
// and no peer to ask. Short-circuits if a concurrent goroutine already latched
// an FT.
func (b *StorageDiff) reloadProactive(ctx context.Context) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	if b.source.Load().fullDiffFrameTable != nil {
		return nil
	}

	return b.reloadSourceLocked(ctx, refreshCauseProactive)
}

// reloadSourceLocked re-fetches the header and reopens upstream. Caller must
// hold refreshMu. The cause is propagated to refreshHeader's telemetry label.
func (b *StorageDiff) reloadSourceLocked(ctx context.Context, cause string) error {
	bid, err := uuid.Parse(b.buildID)
	if err != nil {
		return fmt.Errorf("parse build id %s: %w", b.buildID, err)
	}
	loaded, err := refreshHeader(ctx, b.persistence, bid, b.diffType, cause)
	if err != nil {
		return fmt.Errorf("reloadSourceLocked: load header for build %s: %w", b.buildID, err)
	}
	upstream, _, ft, err := openFromLoadedHeader(ctx, b.persistence, loaded, b.diffType, b.storageObjectType)
	if err != nil {
		return fmt.Errorf("reloadSourceLocked: build %s: %w", b.buildID, err)
	}
	b.source.Store(&source{upstream: upstream, fullDiffFrameTable: ft})

	return nil
}
