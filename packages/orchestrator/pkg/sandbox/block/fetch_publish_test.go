//go:build linux

package block

import (
	"bytes"
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

var errFrameChecksum = errors.New("frame checksum mismatch")

// captureSeekable wraps an upstream like a shared-cache writeback does: every
// batch is read from the inner reader, then copied into the capture. between
// runs in that gap on the first batch.
type captureSeekable struct {
	storage.RangeOpener

	between    func() error
	betweenErr error
	captured   []byte
	closed     chan struct{}
	armed      atomic.Bool
}

func (s *captureSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := s.RangeOpener.OpenRangeReader(ctx, off, length, ft)
	if err != nil || !s.armed.CompareAndSwap(true, false) {
		return r, src, err
	}

	return &captureReader{RangeReader: r, owner: s}, src, nil
}

type captureReader struct {
	storage.RangeReader

	owner *captureSeekable
	reads int
}

func (r *captureReader) Read(p []byte) (int, error) {
	n, err := r.RangeReader.Read(p)
	r.reads++
	if r.reads == 1 && r.owner.between != nil {
		r.owner.betweenErr = r.owner.between()
	}
	if n > 0 {
		r.owner.captured = append(r.owner.captured, p[:n]...)
	}

	return n, err
}

func (r *captureReader) Close(ctx context.Context) (*storage.ReadStats, error) {
	defer close(r.owner.closed)

	return r.RangeReader.Close(ctx)
}

// A shared-cache miss tees what the fetch reads into a writeback buffer after
// the read returns. A punch landing in that gap must not reach the writeback:
// the fetch reads into a private buffer, never the mapping.
func TestChunkerFetch_WritebackCaptureSurvivesPunch(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker, upstream, ft := populatedChunker(t, allChunkerTestCases[1], data)

	_, err := chunker.Discard(0, testBlockSize)
	require.NoError(t, err)

	capture := &captureSeekable{RangeOpener: upstream, closed: make(chan struct{})}
	capture.armed.Store(true)
	capture.between = func() error {
		// The second block is present and lies inside the first batch.
		freed, err := chunker.Discard(testBlockSize, testBlockSize)
		if err == nil && freed.Present != testBlockSize {
			err = errors.New("the present block was not freed")
		}

		return err
	}

	got := make([]byte, 2*testBlockSize)
	_, err = chunker.ReadAt(ctx, got, 0, capture, ft)
	require.NoError(t, err)
	require.Equal(t, data[:2*testBlockSize], got)

	select {
	case <-capture.closed:
	case <-time.After(5 * time.Second):
		t.Fatal("the captured reader was not closed")
	}
	require.NoError(t, capture.betweenErr)
	require.NotEmpty(t, capture.captured)
	require.Equal(t, data[:len(capture.captured)], capture.captured, "the writeback must hold the upstream's bytes, not the punched mapping")
}

// alteringSeekable serves a frame whose decoded output differs from the
// upstream's bytes inside the second block and whose Close fails, as a codec
// that emits before it verifies would.
type alteringSeekable struct {
	storage.RangeOpener

	armed atomic.Bool
}

func (s *alteringSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	r, src, err := s.RangeOpener.OpenRangeReader(ctx, off, length, ft)
	if err != nil || !s.armed.CompareAndSwap(true, false) {
		return r, src, err
	}

	return &alteringReader{RangeReader: r}, src, nil
}

type alteringReader struct {
	storage.RangeReader

	pos int64
}

func (r *alteringReader) Read(p []byte) (int, error) {
	n, err := r.RangeReader.Read(p)
	for i := range n {
		if off := r.pos + int64(i); off >= testBlockSize && off < 2*testBlockSize {
			p[i] ^= 0xFF
		}
	}
	r.pos += int64(n)

	return n, err
}

func (r *alteringReader) Close(ctx context.Context) (*storage.ReadStats, error) {
	stats, _ := r.RangeReader.Close(ctx)

	return stats, errFrameChecksum
}

// A compressed frame spans many blocks. When only one of them was discarded, a
// corrupt frame that alters its output before its checksum fails must leave
// the still-present neighbour byte-equal and the failed block not-present.
func TestChunkerFetch_CorruptFrameLeavesSiblingIntact(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	data := makeTestData(testFileSize)
	chunker, upstream, ft := populatedChunker(t, allChunkerTestCases[0], data)
	require.True(t, ft.IsCompressed())

	_, err := chunker.Discard(0, testBlockSize)
	require.NoError(t, err)
	fetches := upstream.fetchCount.Load()

	altering := &alteringSeekable{RangeOpener: upstream}
	altering.armed.Store(true)
	got := make([]byte, testBlockSize)
	_, err = chunker.ReadAt(ctx, got, 0, altering, ft)
	require.ErrorIs(t, err, errFrameChecksum)
	require.Equal(t, fetches+1, upstream.fetchCount.Load())
	require.False(t, chunker.IsCached(ctx, 0, testBlockSize), "the failed block stays not-present")

	sibling := make([]byte, testBlockSize)
	_, err = chunker.ReadAt(ctx, sibling, testBlockSize, altering, ft)
	require.NoError(t, err)
	require.Equal(t, fetches+1, upstream.fetchCount.Load(), "the present sibling is served from the cache")
	require.Equal(t, data[testBlockSize:2*testBlockSize], sibling, "a failed frame must not overwrite the present sibling")
}

// A refetch streams the whole chunk, but publishes only the blocks the tracker
// does not hold: a present neighbour keeps its bytes even when the upstream
// now serves different ones for it.
func TestChunkerFetch_PublishSkipsPresentBlocks(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(testFileSize)
			chunker, _, _ := populatedChunker(t, tc, data)

			altered := bytes.Clone(data)
			for i := testBlockSize; i < 2*testBlockSize; i++ {
				altered[i] ^= 0xFF
			}
			other, upstream, ft := tc.newChunker(t, altered)
			require.NoError(t, other.Close())

			_, err := chunker.Discard(0, testBlockSize)
			require.NoError(t, err)

			got := make([]byte, 2*testBlockSize)
			_, err = chunker.ReadAt(ctx, got, 0, upstream, ft)
			require.NoError(t, err)
			require.Equal(t, data[:testBlockSize], got[:testBlockSize], "the discarded block is refetched")
			require.Equal(t, data[testBlockSize:2*testBlockSize], got[testBlockSize:], "the present neighbour keeps its bytes")
			require.Eventually(t, func() bool { return len(sessions(chunker)) == 0 }, 5*time.Second, time.Millisecond)

			sibling := make([]byte, testBlockSize)
			_, err = chunker.ReadAt(ctx, sibling, testBlockSize, upstream, ft)
			require.NoError(t, err)
			require.Equal(t, data[testBlockSize:2*testBlockSize], sibling)
		})
	}
}

// openGateSeekable holds the first open it is armed for until released, so a
// test can act while a session exists but has not yet checked its staleness.
type openGateSeekable struct {
	storage.RangeOpener

	armed   atomic.Bool
	opened  chan struct{}
	proceed chan struct{}
}

func (s *openGateSeekable) OpenRangeReader(ctx context.Context, off, length int64, ft *storage.FrameTable) (storage.RangeReader, storage.Source, error) {
	if s.armed.CompareAndSwap(true, false) {
		close(s.opened)
		<-s.proceed
	}

	return s.RangeOpener.OpenRangeReader(ctx, off, length, ft)
}

// A session that a discard stales before its first batch takes no private
// buffer: the frame (or batch) buffer is taken only after the first staleness
// check, so a cancelled session never allocates a frame it cannot publish.
func TestChunkerFetch_StaleBeforeFirstBatchTakesNoBuffer(t *testing.T) {
	t.Parallel()

	for _, tc := range allChunkerTestCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := t.Context()
			data := makeTestData(testFileSize)
			chunker, upstream, ft := tc.newChunker(t, data)
			t.Cleanup(func() { _ = chunker.Close() })
			gate := &openGateSeekable{RangeOpener: upstream, opened: make(chan struct{}), proceed: make(chan struct{})}
			gate.armed.Store(true)
			proceed := sync.OnceFunc(func() { close(gate.proceed) })
			t.Cleanup(proceed)

			taken := make(chan *fetchSession, 4)
			chunker.testHooks = &chunkerTestHooks{bufferTaken: func(s *fetchSession) { taken <- s }}

			readDone := make(chan error, 1)
			go func() {
				buf := make([]byte, testBlockSize)
				_, err := chunker.ReadAt(ctx, buf, 0, gate, ft)
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
			inFlight := sessions(chunker)
			require.Len(t, inFlight, 1)
			stale := inFlight[0]

			// Nothing is present yet, so the discard frees nothing but it
			// overlaps the session, which is stale before its first batch.
			_, err := chunker.Discard(0, testBlockSize)
			require.NoError(t, err)
			require.True(t, isStale(chunker, stale))
			proceed()

			select {
			case err := <-readDone:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("the read did not return")
			}
			require.ErrorIs(t, sessionErr(stale), errFetchStale)
			require.Eventually(t, func() bool { return len(sessions(chunker)) == 0 }, 5*time.Second, time.Millisecond)

			var took []*fetchSession
			for len(taken) > 0 {
				took = append(took, <-taken)
			}
			require.Len(t, took, 1, "only the replacement session takes a buffer")
			require.NotSame(t, stale, took[0], "a session stale before its first batch must not take a buffer")
		})
	}
}
