//go:build linux

package build

import (
	"bytes"
	"context"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

// newPopulatedStorageDiff builds a StorageDiff over an uncompressed in-memory
// upstream, reads the whole file through it so its chunker cache is resident,
// and adds it to a fresh store. It returns the store and the diff's key.
func newPopulatedStorageDiff(t *testing.T, payload []byte) (*DiffStore, DiffStoreKey, *StorageDiff) {
	t.Helper()

	diff := populateStorageDiff(t, uuid.New(), payload)

	store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	t.Cleanup(store.Close)
	store.Add(diff)

	return store, diff.CacheKey(), diff
}

// storageDiffOver builds a StorageDiff for id over an uncompressed in-memory
// upstream; wrap, if set, wraps each range reader the upstream opens, so a
// test can gate a fetch.
func storageDiffOver(t *testing.T, id uuid.UUID, payload []byte, wrap func(io.Reader) io.Reader) *StorageDiff {
	t.Helper()

	const blockSize = int64(4 << 10)

	upstream := storage.NewMockSeekable(t)
	upstream.EXPECT().
		OpenRangeReader(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, off, length int64, _ *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
			end := min(off+length, int64(len(payload)))
			r := io.Reader(bytes.NewReader(payload[off:end]))
			if wrap != nil {
				r = wrap(r)
			}

			return storage.NewRangeReader(io.NopCloser(r)), storage.SourceFS, nil
		})

	metrics, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	diff, err := newStorageDiff(
		t.TempDir(), id.String(), Memfile, storage.MemfileObjectType, blockSize,
		metrics, nil, upstream, int64(len(payload)), storage.UncompressedFullFrameTable, "", &featureflags.Client{},
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = diff.Close() })

	return diff
}

// populateStorageDiff builds a StorageDiff for id over an uncompressed
// in-memory upstream and reads the whole file through it so its chunker
// cache is resident.
func populateStorageDiff(t *testing.T, id uuid.UUID, payload []byte) *StorageDiff {
	t.Helper()

	diff := storageDiffOver(t, id, payload, nil)

	got := make([]byte, len(payload))
	_, err := diff.ReadAt(t.Context(), got, 0, nil)
	require.NoError(t, err)
	require.Equal(t, payload, got)
	// An uncompressed read returns once the session proves the bytes ready;
	// the fetch goroutine marks the tracker a moment later.
	require.Eventually(t, func() bool { return diff.IsCached(t.Context(), 0, int64(len(payload))) }, 5*time.Second, time.Millisecond)

	return diff
}

// gatedReader holds every Read until advance is closed, signalling parked
// as it does.
type gatedReader struct {
	advance <-chan struct{}
	parked  chan<- struct{}
	r       io.Reader
}

func (g *gatedReader) Read(p []byte) (int, error) {
	select {
	case g.parked <- struct{}{}:
	default:
	}
	<-g.advance

	return g.r.Read(p)
}

// waitReturn is what a fetch wait returned and whether the fetch gate was
// open when it did: a wait that holds for the fetch returns only after the
// test opened the gate.
type waitReturn struct {
	err      error
	released bool
}

// startWait runs wait in a goroutine, returns once that goroutine is
// running, and delivers the wait's return with whether gate was closed when
// it returned.
func startWait(gate <-chan struct{}, wait func() error) <-chan waitReturn {
	entered := make(chan struct{})
	done := make(chan waitReturn, 1)
	go func() {
		close(entered)
		err := wait()
		released := false
		select {
		case <-gate:
			released = true
		default:
		}
		done <- waitReturn{err: err, released: released}
	}()
	<-entered

	return done
}

// The storage diff answers the fetch wait and the fetch timeout from its
// chunker: a fetch in flight over the range holds the wait until the fetch
// completes, the wait then returns with the range present, and the timeout
// is the chunker's own.
func TestStorageDiffForwardsTheFetchWait(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	payload := make([]byte, 4<<10)
	parked := make(chan struct{}, 1)
	advance := make(chan struct{})
	diff := storageDiffOver(t, uuid.New(), payload, func(r io.Reader) io.Reader {
		return &gatedReader{advance: advance, parked: parked, r: r}
	})
	release := sync.OnceFunc(func() { close(advance) })
	t.Cleanup(release) // a failed assertion must not leave Close waiting on the gated fetch

	readDone := make(chan error, 1)
	go func() {
		_, err := diff.ReadAt(ctx, make([]byte, len(payload)), 0, nil)
		readDone <- err
	}()
	select {
	case <-parked:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not park at its read gate")
	}

	waitDone := startWait(advance, func() error { return diff.WaitFetched(ctx, 0, int64(len(payload))) })
	select {
	case r := <-waitDone:
		t.Fatalf("the wait returned with the fetch parked at its gate: %v", r.err)
	default:
	}

	release()
	select {
	case r := <-waitDone:
		require.NoError(t, r.err)
		require.True(t, r.released, "the wait returned before the gate opened")
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not return once the fetch completed")
	}
	require.True(t, diff.IsCached(ctx, 0, int64(len(payload))), "a completed session marks its chunk present before it terminates")
	require.NoError(t, <-readDone)
	require.Equal(t, diff.chunker.FetchTimeout(), diff.FetchTimeout())
	require.Positive(t, diff.FetchTimeout())
}

// DiscardRanges over a present, an absent and a pinned entry.
func TestDiffStoreDiscardRanges(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4 << 10)
	payload := make([]byte, 4*blockSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	t.Run("present frees the named runs", func(t *testing.T) {
		t.Parallel()
		store, key, diff := newPopulatedStorageDiff(t, payload)

		before, err := diff.FileSize(t.Context())
		require.NoError(t, err)

		freed, err := store.DiscardRanges(key, []header.BuildMap{
			{Offset: 0, Length: uint64(blockSize)},
			{Offset: uint64(2 * blockSize), Length: uint64(blockSize)},
		})
		require.NoError(t, err)
		require.Equal(t, 2*blockSize, freed.Present)

		after, err := diff.FileSize(t.Context())
		require.NoError(t, err)
		require.Equal(t, before-2*blockSize, after)
		require.False(t, diff.IsCached(t.Context(), 0, blockSize))
		require.True(t, diff.IsCached(t.Context(), blockSize, blockSize), "the untouched run stays cached")
	})

	t.Run("a bad run refuses the whole call", func(t *testing.T) {
		t.Parallel()
		store, key, diff := newPopulatedStorageDiff(t, payload)

		_, err := store.DiscardRanges(key, []header.BuildMap{
			{Offset: 0, Length: uint64(blockSize)},
			{Offset: uint64(blockSize), Length: 1},
		})
		require.ErrorContains(t, err, "misaligned discard")
		require.True(t, diff.IsCached(t.Context(), 0, 4*blockSize), "nothing is punched when one run is refused")
	})

	t.Run("absent is refused and creates nothing", func(t *testing.T) {
		t.Parallel()
		store, _, _ := newPopulatedStorageDiff(t, payload)

		_, err := store.DiscardRanges(GetDiffStoreKey(uuid.NewString(), Memfile), []header.BuildMap{{Offset: 0, Length: uint64(blockSize)}})
		require.ErrorIs(t, err, ErrDiffNotPresent)
	})

	t.Run("pinned is refused", func(t *testing.T) {
		t.Parallel()
		store, key, diff := newPopulatedStorageDiff(t, payload)

		store.Pin(key)
		_, err := store.DiscardRanges(key, []header.BuildMap{{Offset: 0, Length: uint64(blockSize)}})
		require.ErrorIs(t, err, ErrDiffStorePinned)
		require.True(t, diff.IsCached(t.Context(), 0, blockSize), "a refused discard changes nothing")

		store.Unpin(key)
		freed, err := store.DiscardRanges(key, []header.BuildMap{{Offset: 0, Length: uint64(blockSize)}})
		require.NoError(t, err)
		require.Equal(t, blockSize, freed.Present)
	})
}

// DiscardRangesOf punches the handle the caller holds, not whatever the store
// holds under its key, and still honours the pin.
func TestDiffStoreDiscardRangesOf(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4 << 10)
	payload := make([]byte, 4*blockSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	run := []header.BuildMap{{Offset: 0, Length: uint64(blockSize)}}

	t.Run("a held handle the store replaced is the one punched", func(t *testing.T) {
		t.Parallel()
		id := uuid.New()
		held := populateStorageDiff(t, id, payload)
		fresh := populateStorageDiff(t, id, payload)
		store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
		require.NoError(t, err)
		t.Cleanup(store.Close)
		store.Add(fresh)

		freed, err := store.DiscardRangesOf(held, run)
		require.NoError(t, err)
		require.Equal(t, blockSize, freed.Present)
		require.False(t, held.IsCached(t.Context(), 0, blockSize), "the held handle lost the run")
		require.True(t, fresh.IsCached(t.Context(), 0, 4*blockSize), "the store's own entry is untouched")
	})

	t.Run("a held handle the store closed reports the closed cache", func(t *testing.T) {
		t.Parallel()
		store, _, held := newPopulatedStorageDiff(t, payload)
		require.NoError(t, held.Close())

		_, err := store.DiscardRangesOf(held, run)
		var closed *block.CacheClosedError
		require.ErrorAs(t, err, &closed)
	})

	t.Run("pinned is refused", func(t *testing.T) {
		t.Parallel()
		store, key, held := newPopulatedStorageDiff(t, payload)

		store.Pin(key)
		_, err := store.DiscardRangesOf(held, run)
		require.ErrorIs(t, err, ErrDiffStorePinned)
		require.True(t, held.IsCached(t.Context(), 0, blockSize), "a refused discard changes nothing")
	})
}

// Every memfile and rootfs a pause stores is a deferred diff, so the store's
// discard must reach the chunker through it once the seal has resolved, and
// refuse with a typed error while it has not or when the inner diff has no
// upstream.
func TestDeferredDiffDiscard(t *testing.T) {
	t.Parallel()

	const blockSize = int64(4 << 10)
	payload := make([]byte, 4*blockSize)
	for i := range payload {
		payload[i] = byte(i)
	}

	t.Run("resolved forwards to the chunker-backed inner diff", func(t *testing.T) {
		t.Parallel()
		_, key, diff := newPopulatedStorageDiff(t, payload)
		inner := utils.NewSetOnce[Diff]()
		deferred := NewDeferredDiff(key, blockSize, inner)
		require.NoError(t, inner.SetValue(diff))

		store, err := NewDiffStore(cfg.Config{}, &featureflags.Client{}, t.TempDir(), time.Hour, time.Minute)
		require.NoError(t, err)
		t.Cleanup(store.Close)
		store.Add(deferred)

		before, err := diff.FileSize(t.Context())
		require.NoError(t, err)
		freed, err := store.DiscardRanges(key, []header.BuildMap{{Offset: 0, Length: uint64(blockSize)}})
		require.NoError(t, err)
		require.Equal(t, blockSize, freed.Present)
		after, err := diff.FileSize(t.Context())
		require.NoError(t, err)
		require.Equal(t, before-blockSize, after)
		require.False(t, diff.IsCached(t.Context(), 0, blockSize))
		require.True(t, diff.IsCached(t.Context(), blockSize, 3*blockSize))
	})

	t.Run("unresolved is refused", func(t *testing.T) {
		t.Parallel()
		inner := utils.NewSetOnce[Diff]()
		deferred := NewDeferredDiff(GetDiffStoreKey(uuid.NewString(), Memfile), blockSize, inner)

		disc, ok := deferred.(Discardable)
		require.True(t, ok)
		_, err := disc.DiscardRuns([]block.Range{{Start: 0, Size: blockSize}})
		require.ErrorIs(t, err, ErrDiffUnresolved)
	})

	t.Run("failed seal returns its error", func(t *testing.T) {
		t.Parallel()
		inner := utils.NewSetOnce[Diff]()
		deferred := NewDeferredDiff(GetDiffStoreKey(uuid.NewString(), Rootfs), blockSize, inner)
		require.NoError(t, inner.SetError(ErrDeferredSealFailed))

		_, err := deferred.(Discardable).DiscardRuns([]block.Range{{Start: 0, Size: blockSize}})
		require.ErrorIs(t, err, ErrDeferredSealFailed)
	})

	t.Run("inner without an upstream is refused", func(t *testing.T) {
		t.Parallel()
		inner := utils.NewSetOnce[Diff]()
		deferred := NewDeferredDiff(GetDiffStoreKey(uuid.NewString(), Memfile), blockSize, inner)
		require.NoError(t, inner.SetValue(&NoDiff{}))

		_, err := deferred.(Discardable).DiscardRuns([]block.Range{{Start: 0, Size: blockSize}})
		require.ErrorIs(t, err, ErrDiffNotDiscardable)
	})
}
