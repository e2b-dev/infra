//go:build linux

package build

// Schedule a ready-page caller between planning against a real StorageDiff and
// eviction of that diff while its unrelated progressive fetch tail remains held.
import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	blockmetrics "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type closeWaitSeekable struct {
	storage.Seekable

	opens                    atomic.Int32
	tailEntered, tailRelease chan struct{}
	tailOnce                 sync.Once
}

func (s *closeWaitSeekable) releaseTail() { s.tailOnce.Do(func() { close(s.tailRelease) }) }

func (s *closeWaitSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := s.Seekable.OpenRangeReader(ctx, off, length, ft)
	if err != nil || s.opens.Add(1) != 1 {
		return r, src, err
	}

	return &closeWaitReader{RangeReader: r, done: ctx.Done(), cancellationError: ctx.Err, owner: s}, src, nil
}

type closeWaitReader struct {
	storage.RangeReader

	done              <-chan struct{}
	cancellationError func() error
	owner             *closeWaitSeekable
	calls             int
}

func (r *closeWaitReader) Read(p []byte) (int, error) {
	r.calls++
	if r.calls == 2 {
		close(r.owner.tailEntered)
		select {
		case <-r.owner.tailRelease:
		case <-r.done:
			return 0, r.cancellationError()
		}
	}

	return r.RangeReader.Read(p)
}

type plannedCloseWaitDiff struct {
	*StorageDiff

	gateNext                                  atomic.Bool
	planned, proceed, closeEntered, closeDone chan struct{}
	proceedOnce                               sync.Once
	closeErr                                  error
}

func (d *plannedCloseWaitDiff) releaseRead() { d.proceedOnce.Do(func() { close(d.proceed) }) }

func (d *plannedCloseWaitDiff) ReadAt(ctx context.Context, p []byte, off int64, ft *storage.FrameTable) (int, error) {
	if d.gateNext.CompareAndSwap(true, false) {
		close(d.planned)
		// This gate models preemption after File admitted the old Diff. Do not
		// add a fixture-only cancellation boundary absent from StorageDiff.
		<-d.proceed
	}

	return d.StorageDiff.ReadAt(ctx, p, off, ft)
}

func (d *plannedCloseWaitDiff) Close() error {
	close(d.closeEntered)
	d.closeErr = d.StorageDiff.Close()
	close(d.closeDone)

	return d.closeErr
}

type closeWaitReadResult struct {
	n    int
	err  error
	data []byte
}

type closeWaitFixture struct {
	diff     *plannedCloseWaitDiff
	file     *File
	store    *DiffStore
	upstream *closeWaitSeekable
	payload  []byte
}

func newCloseWaitFixture(t *testing.T) closeWaitFixture {
	t.Helper()
	const size = 3 * 16 * 1024 // > default 16KiB progressive batch, < one 4MiB chunk
	id := uuid.New()
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i*37 + 17)
	}
	base := t.TempDir()
	path := filepath.Join(base, id.String(), storage.MemfileName)
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, payload, 0o600))
	provider, err := storage.NewProvider(t.Context(), storage.Spec{Provider: storage.LocalStorageProvider, BasePath: base})
	require.NoError(t, err)
	source, err := provider.OpenSeekable(t.Context(), storage.Paths{BuildID: id.String()}.DataFile(storage.MemfileName, storage.CompressionNone))
	require.NoError(t, err)
	up := &closeWaitSeekable{Seekable: source, tailEntered: make(chan struct{}), tailRelease: make(chan struct{})}
	metrics, err := blockmetrics.NewMetrics(noop.NewMeterProvider())
	require.NoError(t, err)
	flags := &featureflags.Client{}
	require.Equal(t, 16, flags.IntFlag(t.Context(), featureflags.MinChunkerReadSizeKB), "fixture expects unchanged default batching")
	d, err := newStorageDiff(t.TempDir(), id.String(), Memfile, storage.MemfileObjectType, 4096, metrics, provider, up, size, storage.UncompressedFullFrameTable, "", flags)
	require.NoError(t, err)
	observed := &plannedCloseWaitDiff{StorageDiff: d, planned: make(chan struct{}), proceed: make(chan struct{}), closeEntered: make(chan struct{}), closeDone: make(chan struct{})}
	store, err := NewDiffStore(cfg.Config{}, flags, t.TempDir(), time.Hour, time.Minute)
	require.NoError(t, err)
	store.Add(observed)
	h := buildHeader(t, uuid.New(), size, id)
	h.SetBuild(id, header.BuildData{Size: size})
	file := NewFile(h, store, Memfile, provider, metrics)
	f := closeWaitFixture{observed, file, store, up, payload}
	t.Cleanup(func() {
		observed.releaseRead()
		up.releaseTail()
		current, ok := store.Lookup(d.CacheKey())
		var freshPath string
		if ok && current != observed {
			freshPath, _ = current.CachePath(t.Context())
		}
		store.RemoveCache()
		awaitCloseWaitEvent(t, observed.closeDone, "old eviction callback close")
		if freshPath != "" {
			require.Eventually(t, func() bool {
				_, e := os.Stat(freshPath)

				return errors.Is(e, os.ErrNotExist)
			}, 3*time.Second, time.Millisecond, "fresh cache close")
		}
		store.Close()
	})

	return f
}

func awaitCloseWaitEvent(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout: %s", what)
	}
}

func readCloseWaitFile(f closeWaitFixture, ctx context.Context, length int) <-chan closeWaitReadResult {
	out := make(chan closeWaitReadResult, 1)
	go func() {
		p := make([]byte, length)
		n, e := f.file.ReadAt(ctx, p, 0)
		out <- closeWaitReadResult{n, e, p}
	}()

	return out
}

func closeWaitResult(t *testing.T, ch <-chan closeWaitReadResult, what string) closeWaitReadResult {
	t.Helper()
	select {
	case r := <-ch:

		return r
	case <-time.After(3 * time.Second):
		t.Fatalf("timeout: %s", what)

		return closeWaitReadResult{}
	}
}

func waitForCacheCloseWriter(t *testing.T) {
	t.Helper()
	buf := make([]byte, 1<<20)
	require.Eventually(t, func() bool {
		n := runtime.Stack(buf, true)
		for g := range strings.SplitSeq(string(buf[:n]), "\n\n") {
			if strings.Contains(g, "block.(*Cache).Close") && strings.Contains(g, "sync.(*RWMutex).Lock") && strings.Contains(g, "build.(*plannedCloseWaitDiff).Close") {
				return true
			}
		}

		return false
	}, 3*time.Second, time.Millisecond, "actual StorageDiff close did not queue the cache writer")
}

// Slice delegates to the real StorageDiff, with the same plan-preemption gate.
func (d *plannedCloseWaitDiff) Slice(ctx context.Context, off, length int64, ft *storage.FrameTable) ([]byte, error) {
	if d.gateNext.CompareAndSwap(true, false) {
		close(d.planned)
		<-d.proceed
	}

	return d.StorageDiff.Slice(ctx, off, length, ft)
}

// Serial subcases keep the runtime stack observation tied to this fixture's
// unique Close wrapper; all byte, cancellation and fetch effects remain real.
//
//nolint:paralleltest,tparallel // Subcases observe a single pending cache writer in runtime stacks.
func TestStorageDiffReadyReadCancellationBehindEviction(t *testing.T) {
	t.Parallel()
	for _, consumer := range []string{"ChunkerRead", "ChunkerSlice", "StorageDiffRead", "StorageDiffSlice", "FileRead", "FileSlice"} {
		t.Run(consumer, func(t *testing.T) {
			f := newCloseWaitFixture(t)
			sponsor := readCloseWaitFile(f, t.Context(), len(f.payload))
			sponsorJoined := false
			defer func() {
				f.diff.releaseRead()
				f.upstream.releaseTail()
				if !sponsorJoined {
					_ = closeWaitResult(t, sponsor, "shared fetch cleanup")
				}
			}()
			awaitCloseWaitEvent(t, f.upstream.tailEntered, "unrelated progressive fetch tail")
			// Resident data stays immediately readable while only other shared readers exist.
			warm := closeWaitResult(t, readCloseWaitFile(f, t.Context(), 4096), "ready page control")
			require.NoError(t, warm.err)
			require.Equal(t, f.payload[:4096], warm.data)

			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			out := make(chan closeWaitReadResult, 1)
			joined := false
			started := false
			isFile := strings.HasPrefix(consumer, "File")
			startReader := func() { started = true; go func() { out <- readRetainedCloseWaitConsumer(f, ctx, consumer) }() }
			defer func() {
				cancel()
				f.diff.releaseRead()
				f.upstream.releaseTail()
				if started && !joined {
					_ = closeWaitResult(t, out, "admitted reader cleanup")
				}
			}()
			if isFile {
				f.diff.gateNext.Store(true)
				startReader()
				awaitCloseWaitEvent(t, f.diff.planned, "File plan retaining old diff")
			}
			// Production eviction detaches the old entry before its registered callback.
			f.store.cache.Delete(f.diff.CacheKey())
			awaitCloseWaitEvent(t, f.diff.closeEntered, "registered eviction callback")
			waitForCacheCloseWriter(t)
			cancel()
			f.diff.releaseRead()
			if !isFile {
				startReader()
			}
			var result closeWaitReadResult
			returnedBeforeTail := false
			select {
			case result = <-out:
				returnedBeforeTail = true
				joined = true
			case <-time.After(200 * time.Millisecond):
			}
			closeCompletedTooSoon := false
			select {
			case <-f.diff.closeDone:
				closeCompletedTooSoon = true
			default:
			}

			// Join before assertions: rescue release cannot manufacture an early return.
			f.upstream.releaseTail()
			if !joined {
				result = closeWaitResult(t, out, "cancelled reader after tail release")
				joined = true
			}
			healthy := closeWaitResult(t, sponsor, "uncancelled shared fetch sponsor")
			sponsorJoined = true
			awaitCloseWaitEvent(t, f.diff.closeDone, "evicted cache physical close")
			require.NoError(t, healthy.err)
			require.Equal(t, f.payload, healthy.data)
			require.NoError(t, f.diff.closeErr)
			require.False(t, closeCompletedTooSoon, "cache must remain mapped while shared fetch uses it")
			require.True(t, returnedBeforeTail, "caller cancellation waited for an unrelated fetch tail behind eviction Close")
			require.ErrorIs(t, result.err, context.Canceled)
		})
	}
}

func readRetainedCloseWaitConsumer(f closeWaitFixture, ctx context.Context, consumer string) closeWaitReadResult {
	p := make([]byte, 4096)
	var n int
	var err error
	switch consumer {
	case "ChunkerRead":
		n, err = f.diff.chunker.ReadAt(ctx, p, 0, f.upstream, storage.UncompressedFrameTable)
	case "ChunkerSlice":
		p, err = f.diff.chunker.Slice(ctx, 0, 4096, f.upstream, storage.UncompressedFrameTable)
		n = len(p)
	case "StorageDiffRead":
		n, err = f.diff.StorageDiff.ReadAt(ctx, p, 0, storage.UncompressedFrameTable)
	case "StorageDiffSlice":
		p, err = f.diff.StorageDiff.Slice(ctx, 0, 4096, storage.UncompressedFrameTable)
		n = len(p)
	case "FileRead":
		n, err = f.file.ReadAt(ctx, p, 0)
	case "FileSlice":
		p, err = f.file.Slice(ctx, 0, 4096)
		n = len(p)
	default:
		panic("unknown consumer")
	}

	return closeWaitReadResult{n: n, err: err, data: p}
}
