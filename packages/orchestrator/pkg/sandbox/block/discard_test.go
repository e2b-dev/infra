//go:build linux

package block

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var (
	errShortRead = errors.New("short read")
	errTornPage  = errors.New("torn or zero page")
)

type discardResult struct {
	freed DiscardStats
	err   error
}

// cacheSnapshot is everything a refused Discard must leave untouched.
type cacheSnapshot struct {
	dirty, zero, removed, clean []uint32
	fileSize                    int64
	content                     []byte
}

func snapshotCache(t *testing.T, c *Cache) cacheSnapshot {
	t.Helper()

	dirty, zero, removed, clean := c.tracker.ExportStates()
	fileSize, err := c.FileSize(t.Context())
	require.NoError(t, err)
	content := make([]byte, c.size)
	for off := int64(0); off < c.size; off += c.blockSize {
		if !c.isCached(off, c.blockSize) {
			continue
		}
		_, err := c.ReadAt(content[off:off+c.blockSize], off)
		require.NoError(t, err)
	}

	return cacheSnapshot{
		dirty: dirty.ToArray(), zero: zero.ToArray(), removed: removed.ToArray(), clean: clean.ToArray(),
		fileSize: fileSize, content: content,
	}
}

// populatedChunker fetches every byte of data through the chunker so the
// whole file is cached and counted exactly once by the upstream.
func populatedChunker(t *testing.T, tc chunkerTestCase, data []byte) (*Chunker, *fakeSeekable, *storage.FrameTable) {
	t.Helper()

	chunker, upstream, ft := tc.newChunker(t, data)
	t.Cleanup(func() { _ = chunker.Close() })

	got := make([]byte, len(data))
	n, err := chunker.ReadAt(t.Context(), got, 0, upstream, ft)
	require.NoError(t, err)
	require.Len(t, data, n)
	require.Equal(t, data, got)

	// An uncompressed read returns once the session proves the bytes ready;
	// the fetch goroutine marks the tracker a moment later.
	require.Eventually(t, func() bool { return chunker.IsCached(t.Context(), 0, int64(len(data))) }, 5*time.Second, time.Millisecond)

	fake, ok := upstream.(*fakeSeekable)
	require.True(t, ok)

	return chunker, fake, ft
}

// A discarded range is gone from the tracker and the file, and the next
// read brings it back from upstream with exactly one fetch.
func TestChunkerDiscard_RefetchesFromUpstream(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(testFileSize)
			chunker, upstream, ft := populatedChunker(t, tc, data)
			fetches := upstream.fetchCount.Load()

			before, err := chunker.FileSize(ctx)
			require.NoError(t, err)
			require.EqualValues(t, testFileSize, before, "the whole file is allocated after the populate read")

			const off, length = int64(testFrameSize), int64(testFrameSize)
			freed, err := chunker.Discard(off, length)
			require.NoError(t, err)
			require.Equal(t, length, freed.Present)

			after, err := chunker.FileSize(ctx)
			require.NoError(t, err)
			require.Equal(t, before-length, after, "the file must no longer hold the discarded bytes")
			require.False(t, chunker.IsCached(ctx, off, length))
			require.True(t, chunker.IsCached(ctx, 0, off), "blocks before the range stay cached")
			require.True(t, chunker.IsCached(ctx, off+length, testFileSize-off-length), "blocks after the range stay cached")
			require.Equal(t, fetches, upstream.fetchCount.Load(), "discard itself fetches nothing")

			got := make([]byte, length)
			n, err := chunker.ReadAt(ctx, got, off, upstream, ft)
			require.NoError(t, err)
			require.EqualValues(t, length, n)
			require.Equal(t, data[off:off+length], got, "the read must return the upstream's bytes, not the punched zeros")
			require.Equal(t, fetches+1, upstream.fetchCount.Load(), "exactly one fetch for the discarded range")
			require.Eventually(t, func() bool { return chunker.IsCached(ctx, off, length) }, 5*time.Second, time.Millisecond)

			got = make([]byte, testFileSize)
			_, err = chunker.ReadAt(ctx, got, 0, upstream, ft)
			require.NoError(t, err)
			require.Equal(t, data, got)
			require.Equal(t, fetches+1, upstream.fetchCount.Load(), "nothing else needed a fetch")
		})
	}
}

// Unaligned arguments and a cache with no upstream are refused, and a
// refusal changes neither the tracker nor the file.
func TestCacheDiscard_Refusals(t *testing.T) {
	t.Parallel()

	t.Run("unaligned", func(t *testing.T) {
		t.Parallel()
		chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))
		before := snapshotCache(t, chunker.cache)

		for _, args := range [][2]int64{{1, testBlockSize}, {0, testBlockSize + 1}, {-testBlockSize, testBlockSize}, {0, -testBlockSize}} {
			_, err := chunker.Discard(args[0], args[1])
			require.ErrorContains(t, err, "misaligned discard", "off=%d len=%d", args[0], args[1])
		}
		require.Equal(t, before, snapshotCache(t, chunker.cache))
	})

	t.Run("no upstream", func(t *testing.T) {
		t.Parallel()
		data := makeTestData(4 * testBlockSize)
		for _, dirtyFile := range []bool{false, true} {
			cache, err := NewCache(int64(len(data)), testBlockSize, t.TempDir()+"/cache", dirtyFile)
			require.NoError(t, err)
			t.Cleanup(func() { _ = cache.Close() })
			_, err = cache.WriteAt(data, 0)
			require.NoError(t, err)
			before := snapshotCache(t, cache)

			cache.mu.Lock()
			_, _, err = cache.discardLocked([]Range{{Start: 0, Size: testBlockSize}})
			cache.mu.Unlock()
			require.ErrorIs(t, err, ErrCacheNoUpstream, "dirtyFile=%v", dirtyFile)
			require.Equal(t, before, snapshotCache(t, cache))
		}
	})

	t.Run("closed", func(t *testing.T) {
		t.Parallel()
		chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))
		require.NoError(t, chunker.Close())

		_, err := chunker.Discard(0, testBlockSize)
		var closed *CacheClosedError
		require.ErrorAs(t, err, &closed)
	})

	t.Run("beyond the end and not-present ranges free nothing", func(t *testing.T) {
		t.Parallel()
		chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))

		freed, err := chunker.Discard(testFileSize, testBlockSize)
		require.NoError(t, err)
		require.Zero(t, freed.Present)

		freed, err = chunker.Discard(0, testBlockSize)
		require.NoError(t, err)
		require.EqualValues(t, testBlockSize, freed.Present)
		freed, err = chunker.Discard(0, 2*testBlockSize)
		require.NoError(t, err)
		require.EqualValues(t, testBlockSize, freed.Present, "only the still-present second block counts")
	})
}

// Readers racing Discard see whole pages of the upstream's bytes, never
// a punched zero page.
func TestChunkerDiscard_ConcurrentReaders(t *testing.T) {
	t.Parallel()

	t.Run("stress", func(t *testing.T) {
		t.Parallel()

		ctx := t.Context()
		data := makeTestData(testFileSize)
		chunker, upstream, ft := populatedChunker(t, allChunkerTestCases[1], data)
		fetches := upstream.fetchCount.Load()

		const blocks = int64(testFileSize / testBlockSize)
		stop := make(chan struct{})
		g, gctx := errgroup.WithContext(ctx)
		for w := range 4 {
			g.Go(func() error {
				rng := rand.New(rand.NewPCG(uint64(w), 1)) //nolint:gosec // deterministic test data
				buf := make([]byte, 4*testBlockSize)
				for {
					select {
					case <-stop:
						return nil
					case <-gctx.Done():
						return nil
					default:
					}
					off := rng.Int64N(blocks-4) * testBlockSize
					n, err := chunker.ReadAt(ctx, buf, off, upstream, ft)
					if err != nil {
						return err
					}
					if n != len(buf) {
						return errShortRead
					}
					for b := range int64(4) {
						got := buf[b*testBlockSize : (b+1)*testBlockSize]
						want := data[off+b*testBlockSize : off+(b+1)*testBlockSize]
						if string(got) != string(want) {
							return errTornPage
						}
					}
				}
			})
		}
		g.Go(func() error {
			defer close(stop)
			rng := rand.New(rand.NewPCG(99, 1)) //nolint:gosec // deterministic test data
			for range 300 {
				off := rng.Int64N(blocks-8) * testBlockSize
				if _, err := chunker.Discard(off, (1+rng.Int64N(8))*testBlockSize); err != nil {
					return err
				}
				time.Sleep(50 * time.Microsecond)
			}

			return nil
		})
		require.NoError(t, g.Wait())
		require.Greater(t, upstream.fetchCount.Load(), fetches, "discards must have forced refetches")

		got := make([]byte, testFileSize)
		_, err := chunker.ReadAt(ctx, got, 0, upstream, ft)
		require.NoError(t, err)
		require.Equal(t, data, got)
	})
}

// injectSession adds a hand-built session to the chunker's list, re-creating
// the window in which a session that will never run again is still listed.
func injectSession(t *testing.T, chunker *Chunker, s *fetchSession) {
	t.Helper()

	chunker.fetchMu.Lock()
	chunker.fetchSessions = append(chunker.fetchSessions, s)
	chunker.fetchMu.Unlock()
	t.Cleanup(func() {
		chunker.fetchMu.Lock()
		chunker.fetchSessions = nil
		chunker.fetchMu.Unlock()
	})
}

// isStale is the chunker's verdict on s under its session lock.
func isStale(chunker *Chunker, s *fetchSession) bool {
	chunker.fetchMu.Lock()
	defer chunker.fetchMu.Unlock()

	return chunker.staleLocked(s)
}

// sessions is a copy of the chunker's session list.
func sessions(chunker *Chunker) []*fetchSession {
	chunker.fetchMu.Lock()
	defer chunker.fetchMu.Unlock()

	return append([]*fetchSession(nil), chunker.fetchSessions...)
}

// sessionErr is the session's terminal error under its lock.
func sessionErr(s *fetchSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.fetchErr
}

// A discard stales only the sessions whose chunk it overlaps: a session
// streaming another chunk completes and marks it present as before, while one
// streaming the discarded chunk terminates stale and the read fetches again.
func TestChunkerDiscard_StalenessIsRangeScoped(t *testing.T) {
	t.Parallel()

	const chunk = int64(storage.MemoryChunkSize)
	const size = 2 * chunk

	for _, tc := range []struct {
		name       string
		discardOff int64
		stale      bool
	}{
		{name: "another chunk", discardOff: chunk, stale: false},
		{name: "the same chunk", discardOff: testBlockSize, stale: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(int(size))
			cc := newControlledChunker(t, data)
			t.Cleanup(func() { _ = cc.Close() })
			close(cc.advance)
			got := make([]byte, size)
			_, err := cc.ReadAt(ctx, got, 0, cc.file, nil)
			require.NoError(t, err)
			require.Equal(t, data, got)
			require.Eventually(t, func() bool { return cc.IsCached(ctx, 0, size) && len(sessions(cc.Chunker)) == 0 }, 5*time.Second, time.Millisecond)

			// Block 0 becomes a miss; its fetch is gated at the upstream's first read.
			_, err = cc.Discard(0, testBlockSize)
			require.NoError(t, err)
			gate := &testControl{advance: make(chan struct{}), opened: make(chan struct{}, 1)}
			cc.file.ctrl = gate
			release := sync.OnceFunc(func() { close(gate.advance) })
			t.Cleanup(release)
			fetches := cc.file.fetchCount.Load()

			readDone := make(chan error, 1)
			go func() {
				buf := make([]byte, testBlockSize)
				_, err := cc.ReadAt(ctx, buf, 0, cc.file, nil)
				if err == nil && !bytes.Equal(buf, data[:testBlockSize]) {
					err = errTornPage
				}
				readDone <- err
			}()
			select {
			case <-gate.opened:
			case <-time.After(5 * time.Second):
				t.Fatal("the fetch did not reach its upstream")
			}
			inFlight := sessions(cc.Chunker)
			require.Len(t, inFlight, 1)
			s := inFlight[0]
			require.False(t, isStale(cc.Chunker, s))

			_, err = cc.Discard(tc.discardOff, testBlockSize)
			require.NoError(t, err)
			require.Equal(t, tc.stale, isStale(cc.Chunker, s))

			release()
			require.NoError(t, <-readDone)
			require.Eventually(t, func() bool { return len(sessions(cc.Chunker)) == 0 }, 5*time.Second, time.Millisecond)
			if tc.stale {
				require.ErrorIs(t, sessionErr(s), errFetchStale)
				require.Equal(t, fetches+2, cc.file.fetchCount.Load(), "the stale session's bytes are not trusted; the read fetched again")

				return
			}
			require.NoError(t, sessionErr(s))
			require.True(t, cc.IsCached(ctx, 0, chunk), "a discard of another chunk must not stale the session: it marks its chunk present")
			require.False(t, cc.IsCached(ctx, chunk, testBlockSize))
			require.Equal(t, fetches+1, cc.file.fetchCount.Load())
		})
	}
}

// The ring is bounded: a session older than the oldest discard it still
// remembers is stale even when no remembered discard overlaps it, never
// current.
func TestChunkerDiscard_RingBoundIsPinned(t *testing.T) {
	t.Parallel()

	chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))
	s := newFetchSession(0, testBlockSize, chunker.cache)
	injectSession(t, chunker, s)

	const unrelated = testFileSize - testBlockSize
	for range discardRingSize {
		_, err := chunker.Discard(unrelated, testBlockSize)
		require.NoError(t, err)
	}
	require.False(t, isStale(chunker, s), "every remembered discard is unrelated")

	_, err := chunker.Discard(unrelated, testBlockSize)
	require.NoError(t, err)
	require.True(t, isStale(chunker, s), "a session older than the ring's oldest entry cannot be proven current")
}

// A fetch session created before a Discard may vouch for bytes it punched. A
// finished one not yet off the list is never joined: the read fetches again.
// One still streaming is not joined either, and the read waits for it to
// terminate before it fetches again, so the chunk never has two writers.
func TestChunkerDiscard_StaleSessionsAreIgnored(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker, upstream, ft := populatedChunker(t, allChunkerTestCases[1], data)
	fetches := upstream.fetchCount.Load()

	finished := newFetchSession(0, testFileSize, chunker.cache)
	finished.setDone()
	injectSession(t, chunker, finished)
	require.False(t, isStale(chunker, finished))

	_, err := chunker.Discard(0, testBlockSize)
	require.NoError(t, err)
	require.True(t, isStale(chunker, finished), "a session from before the discard is stale")

	got := make([]byte, testBlockSize)
	_, err = chunker.ReadAt(ctx, got, 0, upstream, ft)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], got)
	require.Equal(t, fetches+1, upstream.fetchCount.Load(), "the read must fetch again rather than join a stale finished session")
	require.Eventually(t, func() bool { return len(sessions(chunker)) == 1 }, 5*time.Second, time.Millisecond)

	streaming := newFetchSession(0, testFileSize, chunker.cache)
	injectSession(t, chunker, streaming)
	_, err = chunker.Discard(0, testBlockSize)
	require.NoError(t, err)
	require.True(t, isStale(chunker, streaming))

	waiting := make(chan *fetchSession, 1)
	created := make(chan bool, 1)
	chunker.testHooks = &chunkerTestHooks{
		waitingForWriter: func(stale *fetchSession) { waiting <- stale },
		sessionCreated:   func(*fetchSession) { created <- streaming.isTerminated() },
	}
	readDone := make(chan error, 1)
	go func() {
		buf := make([]byte, testBlockSize)
		_, err := chunker.ReadAt(ctx, buf, 0, upstream, ft)
		if err == nil && !bytes.Equal(buf, data[:testBlockSize]) {
			err = errTornPage
		}
		readDone <- err
	}()
	select {
	case stale := <-waiting:
		require.Same(t, streaming, stale)
	case <-created:
		t.Fatal("a replacement session was created while the stale session was still streaming")
	case <-time.After(5 * time.Second):
		t.Fatal("the read neither waited nor fetched")
	}
	streaming.fail(errors.New("upstream gone"))
	require.True(t, <-created, "the replacement must be created only after the stale session terminated")
	require.NoError(t, <-readDone)
	require.Equal(t, fetches+2, upstream.fetchCount.Load())
}

// A session whose chunk is discarded mid-stream stops at its next read batch
// and terminates stale, never marking the chunk present; a read that needs the
// chunk waits for that termination before a replacement is created, so the
// second writer starts only after the first stopped.
func TestChunkerDiscard_StaleStreamAbortsBeforeReplacement(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	cc := newControlledChunker(t, data)
	t.Cleanup(func() { _ = cc.Close() })
	close(cc.advance)
	got := make([]byte, testFileSize)
	_, err := cc.ReadAt(ctx, got, 0, cc.file, nil)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Eventually(t, func() bool { return cc.IsCached(ctx, 0, testFileSize) && len(sessions(cc.Chunker)) == 0 }, 5*time.Second, time.Millisecond)

	// Block 0 becomes a miss; its fetch is gated inside its first read batch.
	_, err = cc.Discard(0, testBlockSize)
	require.NoError(t, err)
	gate := &testControl{advance: make(chan struct{}), opened: make(chan struct{}, 1)}
	cc.file.ctrl = gate
	release := sync.OnceFunc(func() { close(gate.advance) })
	t.Cleanup(release)
	fetches := cc.file.fetchCount.Load()

	var batches atomic.Int64
	batchEntered := make(chan struct{}, 1)
	waiting := make(chan *fetchSession, 1)
	created := make(chan bool, 2)
	var stale *fetchSession
	cc.testHooks = &chunkerTestHooks{
		beforeBatch: func(*fetchSession, int64) {
			batches.Add(1)
			select {
			case batchEntered <- struct{}{}:
			default:
			}
		},
		waitingForWriter: func(s *fetchSession) { waiting <- s },
		sessionCreated: func(*fetchSession) {
			created <- stale == nil || stale.isTerminated()
		},
	}

	read := func(off int64) <-chan error {
		done := make(chan error, 1)
		go func() {
			buf := make([]byte, testBlockSize)
			_, err := cc.ReadAt(ctx, buf, off, cc.file, nil)
			if err == nil && !bytes.Equal(buf, data[off:off+testBlockSize]) {
				err = errTornPage
			}
			done <- err
		}()

		return done
	}
	first := read(0)
	select {
	case <-gate.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not reach its upstream")
	}
	require.True(t, <-created)
	inFlight := sessions(cc.Chunker)
	require.Len(t, inFlight, 1)
	stale = inFlight[0]
	// The writer passed its staleness check and is inside its first batch,
	// parked on the gate, before the punch lands and the second read arrives.
	select {
	case <-batchEntered:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not enter its first batch")
	}

	_, err = cc.Discard(testBlockSize, testBlockSize)
	require.NoError(t, err)
	require.True(t, isStale(cc.Chunker, stale))

	// A second read of a missing block in the same chunk must park behind the
	// stale writer, which is still inside its first batch.
	second := read(testBlockSize)
	select {
	case s := <-waiting:
		require.Same(t, stale, s)
	case <-created:
		t.Fatal("a replacement session was created while the stale writer was inside a batch")
	case <-time.After(5 * time.Second):
		t.Fatal("the second read neither waited nor fetched")
	}
	require.EqualValues(t, 1, batches.Load())

	release()
	require.NoError(t, <-first)
	require.NoError(t, <-second)
	require.True(t, <-created, "the replacement must start only after the stale writer terminated")
	require.ErrorIs(t, sessionErr(stale), errFetchStale)
	require.Equal(t, fetches+2, cc.file.fetchCount.Load())
	require.Eventually(t, func() bool { return len(sessions(cc.Chunker)) == 0 }, 5*time.Second, time.Millisecond)
	require.True(t, cc.IsCached(ctx, 0, testFileSize), "the replacement marks the chunk present")

	// The batch the stale writer was reading when the punch landed is never
	// published: a stale session writes nothing after the punch.
	require.Zero(t, stale.bytesReady.Load(), "a batch read after the punch must not reach the mapping")
}

// A fetch whose upstream panicked after releasing its first block leaves a
// terminated session whose readiness still covers that block. After a Discard
// of the block a read must fetch again rather than trust that readiness.
func TestChunkerDiscard_PanickedSessionIsIgnored(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker, upstream, ft := populatedChunker(t, allChunkerTestCases[1], data)
	fetches := upstream.fetchCount.Load()

	// The state runFetch's recover leaves behind: one block advanced, then failed.
	panicked := newFetchSession(0, testFileSize, chunker.cache)
	panicked.advance(testBlockSize)
	panicked.failIfRunning(errors.New("fetch panicked: upstream"))
	injectSession(t, chunker, panicked)

	_, err := chunker.Discard(0, testBlockSize)
	require.NoError(t, err)

	got := make([]byte, testBlockSize)
	_, err = chunker.ReadAt(ctx, got, 0, upstream, ft)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], got)
	require.Equal(t, fetches+1, upstream.fetchCount.Load())
}

// panicOnceSeekable serves like fakeSeekable, but the first reader it opens
// panics mid-stream after one block, as a broken upstream would.
type panicOnceSeekable struct {
	fakeSeekable

	armed atomic.Bool
}

func (p *panicOnceSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := p.fakeSeekable.OpenRangeReader(ctx, off, length, ft)
	if err != nil || !p.armed.CompareAndSwap(true, false) {
		return r, src, err
	}

	return &panicOnceReader{RangeReader: r, after: testBlockSize}, src, nil
}

type panicOnceReader struct {
	storage.RangeReader

	after int
	read  int
}

func (r *panicOnceReader) Read(p []byte) (int, error) {
	if r.read >= r.after {
		panic("upstream failed mid-stream")
	}
	n, err := r.RangeReader.Read(p[:min(len(p), r.after-r.read)])
	r.read += n

	return n, err
}

// The same case end to end: an upstream panic mid-fetch, a Discard, then a
// read must fetch again and return the upstream's bytes.
func TestChunkerDiscard_AfterUpstreamPanicRefetches(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker := newTestChunker(t, testFileSize)
	t.Cleanup(func() { _ = chunker.Close() })
	upstream := &panicOnceSeekable{fakeSeekable: fakeSeekable{data: data}}
	upstream.armed.Store(true)

	got := make([]byte, testBlockSize)
	_, err := chunker.ReadAt(ctx, got, 0, upstream, nil)
	require.ErrorContains(t, err, "panicked")
	require.EqualValues(t, 1, upstream.fetchCount.Load())

	_, err = chunker.Discard(0, testBlockSize)
	require.NoError(t, err)

	_, err = chunker.ReadAt(ctx, got, 0, upstream, nil)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], got)
	require.EqualValues(t, 2, upstream.fetchCount.Load())
	require.Eventually(t, func() bool { return chunker.IsCached(ctx, 0, testFileSize) }, 5*time.Second, time.Millisecond)
}

// A fetch blocked on its upstream holds the stream lock for its whole stream.
// A Discard must not wait for it, and a read that was waiting on that fetch
// must still observe its cancellation while the discard runs.
func TestChunkerDiscard_DoesNotWaitForBlockedFetch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	cc := newControlledChunker(t, data)
	t.Cleanup(func() { _ = cc.Close() })
	close(cc.advance)
	got := make([]byte, testFileSize)
	_, err := cc.ReadAt(ctx, got, 0, cc.file, nil)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Eventually(t, func() bool { return cc.IsCached(ctx, 0, testFileSize) }, 5*time.Second, time.Millisecond)

	// Block 0 becomes a miss; the next fetch for it is gated at its first read.
	freed, err := cc.Discard(0, testBlockSize)
	require.NoError(t, err)
	require.EqualValues(t, testBlockSize, freed.Present)
	gate := &testControl{advance: make(chan struct{}), opened: make(chan struct{}, 1)}
	cc.file.ctrl = gate
	release := sync.OnceFunc(func() { close(gate.advance) })
	t.Cleanup(release) // a failed assertion must not leave Close waiting on the gated fetch

	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	readDone := make(chan error, 1)
	go func() {
		_, err := cc.ReadAt(readCtx, make([]byte, testBlockSize), 0, cc.file, nil)
		readDone <- err
	}()
	select {
	case <-gate.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not reach its upstream")
	}

	discardDone := make(chan discardResult, 1)
	go func() {
		freed, err := cc.Discard(testBlockSize, testBlockSize)
		discardDone <- discardResult{freed, err}
	}()
	select {
	case r := <-discardDone:
		require.NoError(t, r.err)
		require.EqualValues(t, testBlockSize, r.freed.Present)
	case <-time.After(2 * time.Second):
		t.Fatal("Discard waited for the fetch blocked on its upstream")
	}

	cancelRead()
	select {
	case err := <-readDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("the cancelled read did not return while the fetch was blocked")
	}

	// The blocked fetch finishes stale: it must not mark its chunk present.
	release()
	require.Eventually(t, func() bool {
		cc.fetchMu.Lock()
		defer cc.fetchMu.Unlock()

		return len(cc.fetchSessions) == 0
	}, 5*time.Second, time.Millisecond)
	require.False(t, cc.IsCached(ctx, 0, testBlockSize))
	fetches := cc.file.fetchCount.Load()

	got = make([]byte, 2*testBlockSize)
	_, err = cc.ReadAt(ctx, got, 0, cc.file, nil)
	require.NoError(t, err)
	require.Equal(t, data[:2*testBlockSize], got)
	require.Equal(t, fetches+1, cc.file.fetchCount.Load(), "the stale fetch's bytes are not trusted; the read fetches again")
}

// A reader that proved its range present and then copied after a Discard
// punched it would return zeros; ReadAt must notice the generation change and
// read again. The hook runs the whole discard between the proof and the copy,
// and the generation must step exactly once per discard: a reader that
// captured an intermediate value of a double bump would accept the zeros.
func TestChunkerReadAt_RetriesAcrossDiscard(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(testFileSize)
			chunker, upstream, ft := populatedChunker(t, tc, data)
			fetches := upstream.fetchCount.Load()
			genBefore := chunker.cache.discardGen.Load()

			proofs := 0
			chunker.testHooks = &chunkerTestHooks{beforeCopy: func(_, _ int64) {
				proofs++
				if proofs > 1 {
					return
				}
				freed, err := chunker.Discard(0, testBlockSize)
				require.NoError(t, err)
				require.EqualValues(t, testBlockSize, freed.Present)
			}}

			got := make([]byte, testBlockSize)
			n, err := chunker.ReadAt(ctx, got, 0, upstream, ft)
			require.NoError(t, err)
			require.Equal(t, int(testBlockSize), n)
			require.Equal(t, data[:testBlockSize], got, "the reader must not return the punched zeros")
			require.Equal(t, 2, proofs, "exactly one retry: the copy after the punch is rejected and the next proof fetches")
			require.Equal(t, fetches+1, upstream.fetchCount.Load())
			require.Equal(t, genBefore+1, chunker.cache.discardGen.Load(), "one bump per discard")
		})
	}
}

// A discard elsewhere in the file between a reader's proof and its copy
// cannot have punched what it copied: the reader keeps the bytes without
// reading again. Only a discard overlapping the read's range forces a retry.
func TestChunkerReadAt_IgnoresUnrelatedDiscard(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(testFileSize)
			chunker, upstream, ft := populatedChunker(t, tc, data)
			fetches := upstream.fetchCount.Load()

			proofs := 0
			discarded := make(chan discardResult, 1)
			chunker.testHooks = &chunkerTestHooks{beforeCopy: func(_, _ int64) {
				proofs++
				if proofs > 1 {
					return
				}
				freed, err := chunker.Discard(testFileSize-testBlockSize, testBlockSize)
				discarded <- discardResult{freed, err}
			}}

			got := make([]byte, testBlockSize)
			n, err := chunker.ReadAt(ctx, got, 0, upstream, ft)
			require.NoError(t, err)
			require.Equal(t, int(testBlockSize), n)
			require.Equal(t, data[:testBlockSize], got)
			r := <-discarded
			require.NoError(t, r.err)
			require.EqualValues(t, testBlockSize, r.freed.Present)
			require.Equal(t, 1, proofs, "a discard that does not overlap the read must not force a retry")
			require.Equal(t, fetches, upstream.fetchCount.Load())
		})
	}
}

// A reader copies under the cache read lock, so the punch must take the write
// lock: while a read lock is held a Discard is parked as a pending writer (a
// TryRLock fails only then), and it lands once the lock is released.
func TestChunkerDiscard_PunchesUnderTheWriteLock(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))

	chunker.cache.mu.RLock()
	release := sync.OnceFunc(chunker.cache.mu.RUnlock)
	t.Cleanup(release) // a failed assertion must not leave Close waiting on the lock
	discardDone := make(chan discardResult, 1)
	go func() {
		freed, err := chunker.Discard(0, testBlockSize)
		discardDone <- discardResult{freed, err}
	}()
	require.Eventually(t, func() bool {
		if chunker.cache.mu.TryRLock() {
			chunker.cache.mu.RUnlock()

			return false
		}

		return true
	}, 5*time.Second, time.Millisecond, "the discard must wait for the write lock while a reader holds the read lock")
	require.True(t, chunker.IsCached(ctx, 0, testBlockSize), "nothing is discarded while the reader holds the lock")
	release()

	r := <-discardDone
	require.NoError(t, r.err)
	require.EqualValues(t, testBlockSize, r.freed.Present)
	require.False(t, chunker.IsCached(ctx, 0, testBlockSize))
}

// A discard holds the cache write lock across its punch. A cancellable
// reader arriving then cannot take the read lock; it must observe its
// cancellation and return context.Canceled before the writer releases, and
// a read after the release must see the refetched bytes.
func TestChunkerDiscard_CancelledReaderReturnsBehindTheLock(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		for _, consumer := range []string{"ReadAt", "Slice"} {
			t.Run(tc.name+"/"+consumer, func(t *testing.T) {
				t.Parallel()

				data := makeTestData(testFileSize)
				chunker, upstream, ft := populatedChunker(t, tc, data)

				entered := make(chan struct{})
				release := make(chan struct{})
				releaseOnce := sync.OnceFunc(func() { close(release) })
				t.Cleanup(releaseOnce) // a failed assertion must not leave Close waiting on the lock
				chunker.testHooks = &chunkerTestHooks{duringDiscard: func() {
					close(entered)
					<-release
				}}
				discardDone := make(chan discardResult, 1)
				go func() {
					freed, err := chunker.Discard(0, testBlockSize)
					discardDone <- discardResult{freed, err}
				}()
				select {
				case <-entered:
				case <-time.After(5 * time.Second):
					t.Fatal("the discard did not reach the cache write lock")
				}

				ctx, cancel := context.WithCancel(t.Context())
				defer cancel()
				type readResult struct {
					data []byte
					err  error
				}
				out := make(chan readResult, 1)
				go func() {
					buf := make([]byte, testBlockSize)
					var err error
					switch consumer {
					case "ReadAt":
						_, err = chunker.ReadAt(ctx, buf, 0, upstream, ft)
					case "Slice":
						buf, err = chunker.Slice(ctx, 0, testBlockSize, upstream, ft)
					}
					out <- readResult{buf, err}
				}()
				select {
				case r := <-out:
					t.Fatalf("the reader returned (%v) while the writer held the lock", r.err)
				case <-time.After(100 * time.Millisecond):
				}
				cancel()
				var result readResult
				select {
				case result = <-out:
				case <-time.After(2 * time.Second):
					t.Fatal("the cancelled reader waited for the writer to release the lock")
				}
				require.ErrorIs(t, result.err, context.Canceled)

				releaseOnce()
				r := <-discardDone
				require.NoError(t, r.err)
				require.EqualValues(t, testBlockSize, r.freed.Present)
				after, err := chunker.Slice(t.Context(), 0, testBlockSize, upstream, ft)
				require.NoError(t, err)
				require.Equal(t, data[:testBlockSize], after)
			})
		}
	}
}

// Chunker.Slice must hand out a copy, never the mapping: the chunker's cache
// is the only refetchable one, and a view into it would read the punched
// zeros after a Discard.
func TestChunkerSlice_ReturnsCopy(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker, upstream, ft := populatedChunker(t, allChunkerTestCases[1], data)

	slice, err := chunker.Slice(ctx, 0, testBlockSize, upstream, ft)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice)

	_, err = chunker.Discard(0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], slice, "the slice must keep its bytes after the range is punched")

	slice[0] ^= 0xFF
	got := make([]byte, testBlockSize)
	_, err = chunker.ReadAt(ctx, got, 0, upstream, ft)
	require.NoError(t, err)
	require.Equal(t, data[:testBlockSize], got, "writing to the slice must not reach the cache")
}

// The bytes a Discard reports must clip a partial last block to the file
// size rather than count it as a whole block.
func TestCacheDiscard_PartialTailCountsItsLength(t *testing.T) {
	t.Parallel()

	const blockSize = int64(64 << 10)
	const tail = int64(4 << 10)
	ctx := t.Context()
	data := makeTestData(int(blockSize + tail))
	chunker, err := NewChunker(&featureflags.Client{}, int64(len(data)), blockSize, t.TempDir()+"/cache", newTestMetrics(t), storage.MemfileObjectType)
	require.NoError(t, err)
	t.Cleanup(func() { _ = chunker.Close() })
	upstream := &fakeSeekable{data: data}

	got := make([]byte, len(data))
	_, err = chunker.ReadAt(ctx, got, 0, upstream, nil)
	require.NoError(t, err)
	require.Equal(t, data, got)
	require.Eventually(t, func() bool { return chunker.IsCached(ctx, 0, int64(len(data))) }, 5*time.Second, time.Millisecond)

	freed, err := chunker.Discard(0, blockSize)
	require.NoError(t, err)
	require.Equal(t, blockSize, freed.Present)

	freed, err = chunker.Discard(0, 2*blockSize)
	require.NoError(t, err)
	require.Equal(t, tail, freed.Present, "only the tail was still present, and it is shorter than a block")

	freed, err = chunker.Discard(0, 2*blockSize)
	require.NoError(t, err)
	require.Zero(t, freed.Present)
}

// One DiscardRuns call is one generation and one ring record however many
// runs it carries, so a large discard cannot push the ring past an unrelated
// in-flight session and stale it.
func TestChunkerDiscardRuns_OneRecordPerCall(t *testing.T) {
	t.Parallel()

	const runs = 1000
	const size = (runs + 1) * testBlockSize
	ctx := t.Context()
	chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(size))
	s := newFetchSession(0, testBlockSize, chunker.cache)
	injectSession(t, chunker, s)
	genBefore := chunker.cache.discardGen.Load()

	dead := make([]Range, 0, runs)
	for i := int64(1); i <= runs; i++ {
		dead = append(dead, Range{Start: i * testBlockSize, Size: testBlockSize})
	}
	freed, err := chunker.DiscardRuns(dead)
	require.NoError(t, err)
	require.EqualValues(t, runs*testBlockSize, freed.Present)
	require.Equal(t, genBefore+1, chunker.cache.discardGen.Load(), "one generation per call")
	require.False(t, isStale(chunker, s), "a session the runs do not overlap stays current")
	require.True(t, chunker.IsCached(ctx, 0, testBlockSize))
	require.False(t, chunker.IsCached(ctx, testBlockSize, runs*testBlockSize))

	_, err = chunker.DiscardRuns([]Range{{Start: 2 * testBlockSize, Size: testBlockSize}, {Start: 0, Size: testBlockSize}})
	require.NoError(t, err)
	require.True(t, isStale(chunker, s), "a record stales the session through any of its runs")
}

// A DiscardRuns call with a bad run is refused whole: nothing is punched and
// no generation is spent.
func TestChunkerDiscardRuns_RefusedWhole(t *testing.T) {
	t.Parallel()

	chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))
	before := snapshotCache(t, chunker.cache)
	genBefore := chunker.cache.discardGen.Load()

	_, err := chunker.DiscardRuns([]Range{{Start: 0, Size: testBlockSize}, {Start: testBlockSize, Size: 1}})
	require.ErrorContains(t, err, "misaligned discard")
	require.Equal(t, before, snapshotCache(t, chunker.cache))
	require.Equal(t, genBefore, chunker.cache.discardGen.Load())

	freed, err := chunker.DiscardRuns(nil)
	require.NoError(t, err)
	require.Zero(t, freed.Present)
	require.Equal(t, genBefore, chunker.cache.discardGen.Load(), "an empty call spends no generation")
}

// Staleness is monotonic: once a session has been judged stale the verdict
// holds even after the ring no longer remembers the discard that caused it.
func TestChunkerStaleness_VerdictIsKept(t *testing.T) {
	t.Parallel()

	chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], makeTestData(testFileSize))
	s := newFetchSession(0, testBlockSize, chunker.cache)
	injectSession(t, chunker, s)

	_, err := chunker.Discard(0, testBlockSize)
	require.NoError(t, err)
	require.True(t, isStale(chunker, s))

	chunker.fetchMu.Lock()
	chunker.discards, chunker.discardNext, chunker.discardEvicted = nil, 0, 0
	chunker.fetchMu.Unlock()
	require.True(t, isStale(chunker, s), "a stale verdict must be kept on the session")
}

// cancelGateSeekable gates the first reader it opens until the gate is
// released or the fetch's context ends, as an upstream honouring its context
// does; shape, if set, gives the cancellation the error a transport reports.
type cancelGateSeekable struct {
	fakeSeekable

	armed   atomic.Bool
	opened  chan struct{}
	release chan struct{}
	shape   func(cancellation error) error
}

func (s *cancelGateSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := s.fakeSeekable.OpenRangeReader(ctx, off, length, ft)
	if err != nil || !s.armed.CompareAndSwap(true, false) {
		return r, src, err
	}
	close(s.opened)
	cancellationError := ctx.Err
	if s.shape != nil {
		cancellationError = func() error { return s.shape(ctx.Err()) }
	}

	return &cancelGateReader{RangeReader: r, done: ctx.Done(), cancellationError: cancellationError, release: s.release}, src, nil
}

type cancelGateReader struct {
	storage.RangeReader

	done              <-chan struct{}
	cancellationError func() error
	release           chan struct{}
}

func (r *cancelGateReader) Read(p []byte) (int, error) {
	select {
	case <-r.release:
	case <-r.done:
		return 0, r.cancellationError()
	}

	return r.RangeReader.Read(p)
}

// cancellationShapes are the errors the production transports surface for a
// cancelled stream: none of them wraps the context error.
var cancellationShapes = []struct {
	name  string
	shape func(cancellation error) error
}{
	{name: "context error"},
	{name: "grpc canceled status", shape: func(cancellation error) error {
		return status.Error(codes.Canceled, cancellation.Error())
	}},
	{name: "http cut body", shape: func(error) error { return io.ErrUnexpectedEOF }},
}

// A session blocked on its upstream is cancelled by an overlapping discard:
// it terminates stale at once, whatever error the transport reports for the
// cancellation, and the waiting read's replacement starts within the
// cancellation latency, not the fetch timeout.
func TestChunkerDiscard_CancelsStaleStream(t *testing.T) {
	t.Parallel()

	for _, tc := range cancellationShapes {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(testFileSize)
			chunker := newTestChunker(t, testFileSize)
			t.Cleanup(func() { _ = chunker.Close() })
			upstream := &cancelGateSeekable{fakeSeekable: fakeSeekable{data: data}, opened: make(chan struct{}), release: make(chan struct{}), shape: tc.shape}
			release := sync.OnceFunc(func() { close(upstream.release) })
			t.Cleanup(release)

			got := make([]byte, testFileSize)
			_, err := chunker.ReadAt(ctx, got, 0, upstream, nil)
			require.NoError(t, err)
			require.Equal(t, data, got)
			require.Eventually(t, func() bool { return chunker.IsCached(ctx, 0, testFileSize) && len(sessions(chunker)) == 0 }, 5*time.Second, time.Millisecond)

			_, err = chunker.Discard(0, testBlockSize)
			require.NoError(t, err)
			upstream.armed.Store(true)
			fetches := upstream.fetchCount.Load()

			created := make(chan time.Time, 2)
			chunker.testHooks = &chunkerTestHooks{sessionCreated: func(*fetchSession) { created <- time.Now() }}
			readDone := make(chan error, 1)
			go func() {
				buf := make([]byte, testBlockSize)
				_, err := chunker.ReadAt(ctx, buf, 0, upstream, nil)
				if err == nil && !bytes.Equal(buf, data[:testBlockSize]) {
					err = errTornPage
				}
				readDone <- err
			}()
			select {
			case <-upstream.opened:
			case <-time.After(5 * time.Second):
				t.Fatal("the fetch did not reach its upstream")
			}
			<-created
			inFlight := sessions(chunker)
			require.Len(t, inFlight, 1)
			stale := inFlight[0]

			discardedAt := time.Now()
			_, err = chunker.Discard(testBlockSize, testBlockSize)
			require.NoError(t, err)

			select {
			case at := <-created:
				require.Less(t, at.Sub(discardedAt), 2*time.Second, "the replacement must start on cancellation, not on the fetch timeout")
			case <-time.After(5 * time.Second):
				t.Fatal("the stale stream was not cancelled: no replacement started while its upstream stayed blocked")
			}
			require.ErrorIs(t, sessionErr(stale), errFetchStale, "a cancelled stale stream terminates stale, so its waiters fetch again")
			require.NoError(t, <-readDone, "the waiter must be served by the replacement, not failed")
			require.Equal(t, fetches+2, upstream.fetchCount.Load(), "the stale stream and its replacement")
			require.Eventually(t, func() bool { return len(sessions(chunker)) == 0 }, 5*time.Second, time.Millisecond)
			require.True(t, chunker.IsCached(ctx, 0, 2*testBlockSize))
		})
	}
}

// Overlapping or adjacent runs are coalesced before the punch: a byte is
// punched once and both figures count it once, whatever the input's order.
func TestChunkerDiscardRuns_OverlappingRunsCountOnce(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker, _, _ := populatedChunker(t, allChunkerTestCases[1], data)

	before, err := chunker.FileSize(ctx)
	require.NoError(t, err)

	const b = int64(testBlockSize)
	stats, err := chunker.DiscardRuns([]Range{
		{Start: 6 * b, Size: b},     // adjacent to the next
		{Start: b, Size: 2 * b},     // overlaps the next
		{Start: 0, Size: 2 * b},     // overlaps the previous
		{Start: 5 * b, Size: b},     // adjacent to the first
		{Start: 5 * b, Size: 2 * b}, // covers the first and the previous
	})
	require.NoError(t, err)
	require.Equal(t, DiscardStats{Present: 5 * b, Punched: 5 * b}, stats, "[0,3b) and [5b,7b), each byte once")

	after, err := chunker.FileSize(ctx)
	require.NoError(t, err)
	require.Equal(t, before-5*b, after)
	require.False(t, chunker.IsCached(ctx, 0, 3*b))
	require.True(t, chunker.IsCached(ctx, 3*b, 2*b))
	require.False(t, chunker.IsCached(ctx, 5*b, 2*b))
	require.True(t, chunker.IsCached(ctx, 7*b, testFileSize-7*b))
}

// Two chunk geometries over one range serialise: a read framed on the
// uncompressed geometry cannot join a current session streaming one
// compressed frame of its chunk, and must wait for it to terminate before it
// creates its own session, so the bytes never have two writers.
func TestChunkerFetch_GeometriesSerialise(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	compFT, compressed := makeCompressedTestData(t, data)
	gated := &cancelGateSeekable{fakeSeekable: fakeSeekable{data: compressed.data}, opened: make(chan struct{}), release: make(chan struct{})}
	gated.armed.Store(true)
	release := sync.OnceFunc(func() { close(gated.release) })
	t.Cleanup(release)
	plain := &fakeSeekable{data: data}
	chunker := newTestChunker(t, testFileSize)
	t.Cleanup(func() { _ = chunker.Close() })

	waiting := make(chan *fetchSession, 1)
	created := make(chan *fetchSession, 2)
	chunker.testHooks = &chunkerTestHooks{
		waitingForWriter: func(s *fetchSession) { waiting <- s },
		sessionCreated:   func(s *fetchSession) { created <- s },
	}
	read := func(upstream storage.RangeOpener, ft *storage.FrameTable) <-chan error {
		done := make(chan error, 1)
		go func() {
			buf := make([]byte, testBlockSize)
			_, err := chunker.ReadAt(ctx, buf, 0, upstream, ft)
			if err == nil && !bytes.Equal(buf, data[:testBlockSize]) {
				err = errTornPage
			}
			done <- err
		}()

		return done
	}

	frameRead := read(gated, compFT)
	select {
	case <-gated.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the frame fetch did not reach its upstream")
	}
	frameSession := <-created
	require.EqualValues(t, testFrameSize, frameSession.chunkLen)
	require.False(t, isStale(chunker, frameSession))

	chunkRead := read(plain, nil)
	select {
	case s := <-waiting:
		require.Same(t, frameSession, s)
	case s := <-created:
		t.Fatalf("a session over [%d, %d) was created while an overlapping writer was streaming", s.chunkOff, s.chunkOff+s.chunkLen)
	case <-time.After(5 * time.Second):
		t.Fatal("the chunk read neither waited nor fetched")
	}

	release()
	require.NoError(t, <-frameRead)
	chunkSession := <-created
	require.True(t, frameSession.isTerminated(), "the second writer must start only after the first terminated")
	require.EqualValues(t, testFileSize, chunkSession.chunkLen)
	require.NoError(t, <-chunkRead)
	require.Eventually(t, func() bool { return len(sessions(chunker)) == 0 }, 5*time.Second, time.Millisecond)
	require.True(t, chunker.IsCached(ctx, 0, testFileSize))
	got := make([]byte, testFileSize)
	_, err := chunker.ReadAt(ctx, got, 0, plain, nil)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

var errUpstreamBroken = errors.New("upstream returned a broken frame")

// brokenGateSeekable gates the first reader it opens until released, then
// fails the read with errUpstreamBroken whatever the fetch's context says, as
// an upstream whose own failure races a discard does.
type brokenGateSeekable struct {
	fakeSeekable

	armed   atomic.Bool
	opened  chan struct{}
	release chan struct{}
}

func (s *brokenGateSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := s.fakeSeekable.OpenRangeReader(ctx, off, length, ft)
	if err != nil || !s.armed.CompareAndSwap(true, false) {
		return r, src, err
	}
	close(s.opened)

	return &brokenGateReader{RangeReader: r, release: s.release}, src, nil
}

type brokenGateReader struct {
	storage.RangeReader

	release chan struct{}
}

func (r *brokenGateReader) Read([]byte) (int, error) {
	<-r.release

	return 0, errUpstreamBroken
}

// An upstream failure on a session no discard overlapped is reported as that
// failure, not as staleness, even when a discard elsewhere in the file bumped
// the generation meanwhile: the session's waiters must not retry against the
// broken upstream, and the error keeps its cause.
func TestChunkerFetch_UpstreamErrorOnCurrentSessionKeepsCause(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	const size = 2 * storage.MemoryChunkSize
	chunker := newTestChunker(t, size)
	t.Cleanup(func() { _ = chunker.Close() })
	upstream := &brokenGateSeekable{fakeSeekable: fakeSeekable{data: make([]byte, size)}, opened: make(chan struct{}), release: make(chan struct{})}
	release := sync.OnceFunc(func() { close(upstream.release) })
	t.Cleanup(release)
	upstream.armed.Store(true)

	readDone := make(chan error, 1)
	go func() {
		_, err := chunker.ReadAt(ctx, make([]byte, testBlockSize), 0, upstream, nil)
		readDone <- err
	}()
	select {
	case <-upstream.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not reach its upstream")
	}
	inFlight := sessions(chunker)
	require.Len(t, inFlight, 1)
	broken := inFlight[0]

	// The second chunk is discarded while the first is at its upstream.
	_, err := chunker.Discard(storage.MemoryChunkSize, testBlockSize)
	require.NoError(t, err)
	require.False(t, isStale(chunker, broken), "the discard does not overlap the session's chunk")
	release()

	select {
	case err := <-readDone:
		require.ErrorIs(t, err, errUpstreamBroken, "an upstream failure during a discard must keep its cause")
		require.NotErrorIs(t, err, errFetchStale)
	case <-time.After(5 * time.Second):
		t.Fatal("the read did not return")
	}
	require.ErrorIs(t, sessionErr(broken), errUpstreamBroken)
	require.EqualValues(t, 1, upstream.fetchCount.Load(), "the waiters must not retry against the broken upstream")
}

// onePPool models sync.Pool on one P: Put fills the private slot first, then
// the shared stack, and Get drains the private slot first. It is deterministic
// where sync.Pool is not (under the race detector Put drops items at random).
type onePPool struct {
	private any
	shared  []any
}

func (p *onePPool) Get() any {
	if p.private != nil {
		x := p.private
		p.private = nil

		return x
	}
	if n := len(p.shared); n > 0 {
		x := p.shared[n-1]
		p.shared = p.shared[:n-1]

		return x
	}

	return nil
}

func (p *onePPool) Put(x any) {
	if p.private == nil {
		p.private = x

		return
	}
	p.shared = append(p.shared, x)
}

// A pool fed alternately with a short and a full buffer keeps serving the full
// one: an undersized pop is replaced, not re-inserted into the private slot
// every Get consults first, so a full request does not allocate on every
// round.
func TestGetBuffer_UndersizedPopIsReplaced(t *testing.T) { //nolint:paralleltest // the allocation count is global
	const short, full = int64(testBlockSize), int64(testFrameSize)
	pool := &onePPool{}
	var wrongLen bool
	allocs := testing.AllocsPerRun(1000, func() {
		b := getBuffer(pool, short)
		pool.Put(b)
		b = getBuffer(pool, full)
		wrongLen = wrongLen || int64(len(*b)) != full
		pool.Put(b)
	})
	require.False(t, wrongLen, "every pop must be sliced to its request")
	require.Zero(t, allocs, "a full request after a short one must be served from the pool")
}

var errBodyChecksum = errors.New("body checksum mismatch")

// discardingSeekable serves one partial read that discards the session's
// chunk from inside the read and then fails with errBodyChecksum, so the
// publish that follows is refused as stale.
type discardingSeekable struct {
	fakeSeekable

	chunker *Chunker
	partial int
}

func (s *discardingSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := s.fakeSeekable.OpenRangeReader(ctx, off, length, ft)
	if err != nil {
		return r, src, err
	}

	return &discardingReader{RangeReader: r, chunker: s.chunker, partial: s.partial}, src, nil
}

type discardingReader struct {
	storage.RangeReader

	chunker *Chunker
	partial int
	err     error
}

func (r *discardingReader) Read(p []byte) (int, error) {
	n, err := r.RangeReader.Read(p[:min(len(p), r.partial)])
	if err != nil {
		return n, err
	}
	if _, r.err = r.chunker.Discard(0, testBlockSize); r.err != nil {
		return n, r.err
	}

	return n, errBodyChecksum
}

// A read that returned bytes and an error, overlapped by a discard, reports
// the read's error: the stale refusal keeps the bytes out of the mapping but
// does not replace the upstream's failure with staleness.
func TestChunkerFetch_PartialReadErrorWinsOverStalePublish(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker := newTestChunker(t, testFileSize)
	t.Cleanup(func() { _ = chunker.Close() })
	upstream := &discardingSeekable{fakeSeekable: fakeSeekable{data: data}, chunker: chunker, partial: 2 * testBlockSize}

	s := newFetchSession(0, testFileSize, chunker.cache)
	injectSession(t, chunker, s)

	_, frame, err := chunker.progressiveFetch(ctx, s, upstream, nil)
	require.Nil(t, frame)
	require.ErrorIs(t, err, errBodyChecksum, "the read's own failure must be reported")
	require.NotErrorIs(t, err, errFetchStale)
	require.True(t, isStale(chunker, s), "the discard overlapped the session's chunk")
	require.Zero(t, s.bytesReady.Load(), "a refused publish advances nothing")

	chunker.cache.mu.RLock()
	published := bytes.Clone((*chunker.cache.mmap)[:2*testBlockSize])
	chunker.cache.mu.RUnlock()
	require.Equal(t, make([]byte, 2*testBlockSize), published, "a stale session's bytes never reach the mapping")
}

// A discard reports both figures: the bytes the tracker held as present (the
// savings) and the extent it punched. A block a session has published but not
// yet marked present is punched, so it counts in the extent and not the savings.
func TestChunkerDiscard_ReportsPunchedExtent(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker := newTestChunker(t, testFileSize)
	t.Cleanup(func() { _ = chunker.Close() })
	upstream := &fakeSeekable{data: data}

	// At the second batch the first is published and still unmarked: the
	// session marks its chunk only once the whole stream is done.
	var once sync.Once
	midStream := make(chan discardResult, 1)
	chunker.testHooks = &chunkerTestHooks{beforeBatch: func(_ *fetchSession, written int64) {
		if written == 0 {
			return
		}
		once.Do(func() {
			stats, err := chunker.Discard(0, written)
			midStream <- discardResult{stats, err}
		})
	}}

	got := make([]byte, testFileSize)
	_, err := chunker.ReadAt(ctx, got, 0, upstream, nil)
	require.NoError(t, err)
	require.Equal(t, data, got)

	r := <-midStream
	require.NoError(t, r.err)
	require.Zero(t, r.freed.Present, "a published but unmarked block is not present in the tracker")
	require.Positive(t, r.freed.Punched)
	require.Zero(t, r.freed.Punched%testBlockSize)

	require.Eventually(t, func() bool { return chunker.IsCached(ctx, 0, testFileSize) && len(sessions(chunker)) == 0 }, 5*time.Second, time.Millisecond)
	stats, err := chunker.Discard(0, testBlockSize)
	require.NoError(t, err)
	require.Equal(t, DiscardStats{Present: testBlockSize, Punched: testBlockSize}, stats, "a present block counts in both figures")
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

// A fetch in flight over the range holds WaitFetched until its session
// terminates; a completed session marked its chunk present first, so the
// tracker holds the range when the wait returns.
func TestChunkerWaitFetched_ReturnsOnceTheSessionCompletes(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	cc := newControlledChunker(t, data)
	t.Cleanup(func() { _ = cc.Close() })
	release := sync.OnceFunc(func() { close(cc.advance) })
	t.Cleanup(release) // a failed assertion must not leave Close waiting on the gated fetch

	readDone := make(chan error, 1)
	go func() {
		_, err := cc.ReadAt(ctx, make([]byte, testBlockSize), 0, cc.file, nil)
		readDone <- err
	}()
	select {
	case <-cc.parked:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not park at its read gate")
	}

	waitDone := startWait(cc.advance, func() error { return cc.WaitFetched(ctx, 0, testBlockSize) })
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
	require.True(t, cc.IsCached(ctx, 0, testBlockSize), "a completed session marks its chunk present before it terminates")
	require.NoError(t, <-readDone)
}

// A session staled by a discard holds the wait until it terminates, as its
// bytes are in flight until then; it terminates without marking its chunk.
func TestChunkerWaitFetched_ReturnsOnceAStaleSessionTerminates(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	cc := newControlledChunker(t, data)
	t.Cleanup(func() { _ = cc.Close() })
	release := sync.OnceFunc(func() { close(cc.advance) })
	t.Cleanup(release) // a failed assertion must not leave Close waiting on the gated fetch

	readCtx, cancelRead := context.WithCancel(ctx)
	defer cancelRead()
	readDone := make(chan error, 1)
	go func() {
		_, err := cc.ReadAt(readCtx, make([]byte, testBlockSize), 0, cc.file, nil)
		readDone <- err
	}()
	// The fetch is parked at its read gate, past the staleness check of its
	// first batch, before the discard lands: staled earlier, it would
	// terminate without reading and the wait would not have to hold.
	select {
	case <-cc.parked:
	case <-time.After(5 * time.Second):
		t.Fatal("the fetch did not park at its read gate")
	}
	waitDone := startWait(cc.advance, func() error { return cc.WaitFetched(ctx, 0, testBlockSize) })

	// The discard stales the session, which stays listed and running,
	// parked at its gate; the reader is cancelled so that no replacement
	// session is created once the stale one terminates.
	_, err := cc.Discard(0, testBlockSize)
	require.NoError(t, err)
	cancelRead()
	select {
	case err := <-readDone:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(5 * time.Second):
		t.Fatal("the cancelled read did not return")
	}
	select {
	case r := <-waitDone:
		t.Fatalf("the wait returned with the stale fetch parked at its gate: %v", r.err)
	default:
	}

	release()
	select {
	case r := <-waitDone:
		require.NoError(t, r.err)
		require.True(t, r.released, "the wait returned before the gate opened")
	case <-time.After(5 * time.Second):
		t.Fatal("the wait did not return once the stale fetch terminated")
	}
	require.False(t, cc.IsCached(ctx, 0, testBlockSize), "a stale session never marks its chunk")
}

// A zero-length range overlaps nothing, so a wait over it returns at once
// with a session running over its offset, as a presence query over it is
// true at once.
func TestChunkerWaitFetched_ZeroLengthReturnsAtOnce(t *testing.T) {
	t.Parallel()

	chunker := newTestChunker(t, testFileSize)
	t.Cleanup(func() { _ = chunker.Close() })
	s := newFetchSession(0, testBlockSize, chunker.cache)
	injectSession(t, chunker, s)

	done := make(chan error, 1)
	go func() {
		for _, off := range []int64{0, 1, testBlockSize / 2} {
			if err := chunker.WaitFetched(t.Context(), off, 0); err != nil {
				done <- fmt.Errorf("offset %d: %w", off, err)

				return
			}
		}
		done <- nil
	}()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("the wait blocked on a zero-length range")
	}
	require.Nil(t, chunker.runningSession(1, 0))
	require.False(t, s.isTerminated(), "the session is still running")
}

// A session outside the range does not hold the wait, and a context that
// ends first returns its error while the session is still running.
func TestChunkerWaitFetched_RangeAndContext(t *testing.T) {
	t.Parallel()

	chunker := newTestChunker(t, testFileSize)
	t.Cleanup(func() { _ = chunker.Close() })
	s := newFetchSession(0, testBlockSize, chunker.cache)
	injectSession(t, chunker, s)

	require.NoError(t, chunker.WaitFetched(t.Context(), testBlockSize, testBlockSize), "a session outside the range does not hold the wait")

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	time.AfterFunc(50*time.Millisecond, cancel)
	require.ErrorIs(t, chunker.WaitFetched(ctx, 0, testBlockSize), context.Canceled)
	require.False(t, s.isTerminated(), "the session is still running")
}
