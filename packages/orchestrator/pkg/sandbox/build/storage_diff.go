//go:build linux

package build

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const (
	refreshCauseProactive        = "proactive"
	refreshCausePeerTransitioned = "peer_transitioned"
)

// source carries the StorageDiff's current routing state. upstream is always
// non-nil after construction but may be switched once over the lifetime; the ft
// pointer's nil/empty/non-empty state encodes the lifecycle:
//
//	ft == nil                                  not authoritative. may trigger refresh logic.
//	ft == storage.UncompressedFullFrameTable   authoritatively uncompressed (only set by refresh)
//	ft non-empty                               authoritatively compressed with the bound full-file FT
//
// fullDiffFrameTable is *FullFrameTable rather than *FrameTable: this is the
// one place in the read path where we hold an upcasted full table. The
// invariant — builds[self] for an ancestor we just refreshed is a complete
// table, never a trimmed one — is documented at (*header.Header).SelfBuildData.
// Everywhere else, FrameTables are treated as potentially partial
// (per-mapping, trimmed).
type source struct {
	upstream           storage.RangeOpener
	fullDiffFrameTable *storage.FullFrameTable
}

type StorageDiff struct {
	chunker           *block.Chunker
	cachePath         string
	cacheKey          DiffStoreKey
	buildID           string
	diffType          DiffType
	storageObjectType storage.SeekableObjectType

	blockSize    int64
	metrics      blockmetrics.Metrics
	persistence  storage.StorageProvider
	isActivePeer IsActivePeer

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
	isActivePeer IsActivePeer,
	upstream storage.Seekable,
	uncompressedSize int64,
	initialFT *storage.FullFrameTable,
	ff *featureflags.Client,
) (*StorageDiff, error) {
	cachePath := GenerateDiffCachePath(basePath, buildID, diffType)
	c, err := block.NewChunker(ff, uncompressedSize, blockSize, cachePath, metrics)
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
		isActivePeer:      isActivePeer,
		chunker:           c,
		cacheKey:          GetDiffStoreKey(buildID, diffType),
	}
	d.source.Store(&source{upstream: upstream, fullDiffFrameTable: initialFT})

	return d, nil
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

	return b.chunker.ReadAt(ctx, p, off, up, ft)
}

func (b *StorageDiff) Slice(ctx context.Context, off, length int64, callerFT *storage.FrameTable) ([]byte, error) {
	up, ft, err := b.resolve(ctx, callerFT)
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

func refreshBuildHeader(ctx context.Context, persistence storage.StorageProvider, buildID uuid.UUID, diffType DiffType, cause string) (*header.Header, error) {
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
	if b.isActivePeer != nil && b.isActivePeer(b.buildID) {
		// Peer-active regime: upstream is peer-routed and serves uncompressed
		// by basic name. We deliberately do NOT refresh here — the storage
		// header may not exist yet and we do not handle ErrNotFound.
		return cur.upstream, storage.UncompressedFrameTable, nil
	}
	if err := b.reloadSource(ctx, refreshCauseProactive); err != nil {
		return nil, nil, fmt.Errorf("resolve: %w", err)
	}
	cur = b.source.Load()

	return cur.upstream, cur.fullDiffFrameTable.Table(), nil
}

// RefreshSource reloads the build's header, latches the authoritative FT, and
// reopens upstream at the resulting CT path. Called by readSegment after a
// PeerTransitionedError. Idempotent: once the source latch is populated, the
// post-refresh upstream is base-routed (no peer wrapper) and cannot emit
// further PeerTransitionedErrors, so a second call is a no-op.
func (b *StorageDiff) RefreshSource(ctx context.Context) error {
	return b.reloadSource(ctx, refreshCausePeerTransitioned)
}

// reloadSource is the idempotent ensure-latched entry. Both RefreshSource
// (PeerTransitionedError) and resolve (read-time peer-left fallback) funnel
// through it; the cause attribute distinguishes them in telemetry. A concurrent
// caller that wins the mutex short-circuits when the latch is already
// populated, so parallel segment reads on a fresh StorageDiff pay only one
// header fetch.
func (b *StorageDiff) reloadSource(ctx context.Context, cause string) error {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()
	if b.source.Load().fullDiffFrameTable != nil {
		return nil
	}

	return b.reloadSourceLocked(ctx, cause)
}

// reloadSourceLocked re-fetches the header and reopens upstream. Caller must
// hold refreshMu.
//
// V4+ headers on storage always carry a self entry — set unconditionally
// before publish. A missing self entry here can only come from P2P routing
// returning a still-uploading peer's incomplete header. Treating it as "no
// FrameData = uncompressed" would silently corrupt reads of a compressed file.
// Fail loudly; the read path will retry when the peer transitions and the
// storage-authoritative header is available.
//
// V3 never reaches reloadSourceLocked: getBuild's V3 branch latches an
// authoritative empty &{} FT at construction, so resolve short-circuits and
// reloadSource is never called; V3 builds aren't peer-routed so
// PeerTransitionedError never fires against them either.
func (b *StorageDiff) reloadSourceLocked(ctx context.Context, cause string) error {
	bid, err := uuid.Parse(b.buildID)
	if err != nil {
		return fmt.Errorf("parse build id %s: %w", b.buildID, err)
	}
	loaded, err := refreshBuildHeader(ctx, b.persistence, bid, b.diffType, cause)
	if err != nil {
		return fmt.Errorf("reloadSourceLocked: load header for build %s (cause=%s): %w", b.buildID, cause, err)
	}
	_, ft, err := loaded.SelfBuildData()
	if err != nil {
		return fmt.Errorf("reloadSourceLocked: build %s (cause=%s): %w", b.buildID, cause, err)
	}
	newPath := storage.Paths{BuildID: b.buildID}.DataFile(string(b.diffType), ft.Table().CompressionType())
	newObj, err := b.persistence.OpenSeekable(ctx, newPath, b.storageObjectType)
	if err != nil {
		return fmt.Errorf("reloadSourceLocked: reopen upstream for build %s at %s (cause=%s): %w", b.buildID, newPath, cause, err)
	}

	b.source.Store(&source{upstream: newObj, fullDiffFrameTable: ft})

	return nil
}
