//go:build linux

package build

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// authoritativeFullFrameTable: nil = not fetched; empty = uncompressed.
type source struct {
	upstream                    storage.RangeOpener
	authoritativeFullFrameTable *storage.FrameTable
}

// StorageDiff opens at the parent's hinted CT, refreshing from this build's
// own header sidecar if the hint turns out stale.
type StorageDiff struct {
	chunker           *block.Chunker
	cachePath         string
	cacheKey          DiffStoreKey
	buildID           string
	diffType          DiffType
	storageObjectType storage.SeekableObjectType

	blockSize        int64
	metrics          blockmetrics.Metrics
	persistence      storage.StorageProvider
	featureFlags     *featureflags.Client
	uncompressedSize int64
	// initCT picks Init's storage path; never reaches read decoding.
	initCT storage.CompressionType

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

func newStorageDiff(
	basePath string,
	buildId string,
	diffType DiffType,
	blockSize int64,
	metrics blockmetrics.Metrics,
	persistence storage.StorageProvider,
	uncompressedSize int64,
	hintCT storage.CompressionType,
	ff *featureflags.Client,
) (*StorageDiff, error) {
	storageObjectType, ok := storageObjectType(diffType)
	if !ok {
		return nil, UnknownDiffTypeError{diffType}
	}

	cachePath := GenerateDiffCachePath(basePath, buildId, diffType)

	d := &StorageDiff{
		buildID:           buildId,
		diffType:          diffType,
		storageObjectType: storageObjectType,
		cachePath:         cachePath,
		blockSize:         blockSize,
		metrics:           metrics,
		persistence:       persistence,
		featureFlags:      ff,
		uncompressedSize:  uncompressedSize,
		cacheKey:          GetDiffStoreKey(buildId, diffType),
		initCT:            hintCT,
	}
	// Pre-populate so source.Load() is never nil.
	d.source.Store(&source{})

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

func (b *StorageDiff) Init(ctx context.Context) error {
	storagePath := storage.Paths{BuildID: b.buildID}.DataFile(string(b.diffType), b.initCT)
	obj, err := b.persistence.OpenSeekable(ctx, storagePath, b.storageObjectType)
	if err != nil {
		return err
	}

	size := b.uncompressedSize
	if size == 0 {
		size, err = obj.Size(ctx)
		if err != nil {
			return fmt.Errorf("failed to get object size: %w", err)
		}
	}

	c, err := block.NewChunker(b.featureFlags, size, b.blockSize, b.cachePath, b.metrics)
	if err != nil {
		return fmt.Errorf("failed to create chunker: %w", err)
	}

	b.chunker = c
	b.source.Store(&source{upstream: obj})

	return nil
}

// resolveSource: authoritative FT (if bound) wins over caller's hint.
func (b *StorageDiff) resolveSource(callerFT *storage.FrameTable) (storage.RangeOpener, *storage.FrameTable) {
	cur := b.source.Load()
	ft := callerFT
	if cur.authoritativeFullFrameTable != nil {
		ft = cur.authoritativeFullFrameTable
	}

	return cur.upstream, ft
}

func (b *StorageDiff) FrameTable() *storage.FrameTable {
	return b.source.Load().authoritativeFullFrameTable
}

func (b *StorageDiff) Close() error {
	if b.chunker == nil {
		return nil
	}

	return b.chunker.Close()
}

func (b *StorageDiff) ReadAt(ctx context.Context, p []byte, off int64, callerFT *storage.FrameTable) (int, error) {
	up, ft := b.resolveSource(callerFT)

	return b.chunker.ReadAt(ctx, p, off, up, ft)
}

func (b *StorageDiff) Slice(ctx context.Context, off, length int64, callerFT *storage.FrameTable) ([]byte, error) {
	up, ft := b.resolveSource(callerFT)

	return b.chunker.Slice(ctx, off, length, up, ft)
}

// RefreshFrameTable binds the authoritative full self-FT from this build's
// own header sidecar and reopens the upstream at the resulting CT. Idempotent
// one-shot latch; post-Init only. Returns the loaded header on success so
// the caller can promote it (e.g. swap into File.header on a self-uuid
// match). Returns (nil, nil) when the latch short-circuits.
func (b *StorageDiff) RefreshFrameTable(ctx context.Context) (*header.Header, error) {
	b.refreshMu.Lock()
	defer b.refreshMu.Unlock()

	if b.source.Load().authoritativeFullFrameTable != nil {
		return nil, nil
	}

	headerPath := storage.Paths{BuildID: b.buildID}.HeaderFile(string(b.diffType))
	h, err := header.LoadHeader(ctx, b.persistence, headerPath)
	if err != nil {
		return nil, fmt.Errorf("load own header for build %s: %w", b.buildID, err)
	}

	bid, err := uuid.Parse(b.buildID)
	if err != nil {
		return nil, fmt.Errorf("parse build id %s: %w", b.buildID, err)
	}
	ft := h.GetBuildFrameData(bid)
	if ft == nil {
		// Own header missing its own entry — shouldn't happen, but fall
		// through to an empty FT so the latch fires.
		ft = &storage.FrameTable{}
	}

	newPath := storage.Paths{BuildID: b.buildID}.DataFile(string(b.diffType), ft.CompressionType())
	newObj, err := b.persistence.OpenSeekable(ctx, newPath, b.storageObjectType)
	if err != nil {
		return nil, fmt.Errorf("open upstream at %s: %w", newPath, err)
	}

	// ft was just extracted from the header (so is authoritative) for self-UUID
	// (so is full, covers the entire diff).
	b.source.Store(&source{upstream: newObj, authoritativeFullFrameTable: ft})

	return h, nil
}

// The local file might not be synced.
func (b *StorageDiff) CachePath(context.Context) (string, error) {
	return b.cachePath, nil
}

func (b *StorageDiff) FileSize(ctx context.Context) (int64, error) {
	if b.chunker == nil {
		return 0, nil
	}

	return b.chunker.FileSize(ctx)
}

func (b *StorageDiff) Size(_ context.Context) (int64, error) {
	if b.chunker == nil {
		return 0, nil
	}

	return b.chunker.Size(), nil
}

func (b *StorageDiff) BlockSize() int64 {
	return b.blockSize
}

// IsCached reports whether [off, off+length) is in the chunker's local cache.
// Returns false if the chunker hasn't been Init'd yet (would otherwise trigger
// OpenSeekable). Side-effect-free.
func (b *StorageDiff) IsCached(ctx context.Context, off, length int64) bool {
	if b.chunker == nil {
		return false
	}

	return b.chunker.IsCached(ctx, off, length)
}
