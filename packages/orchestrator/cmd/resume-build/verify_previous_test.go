package main

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/build"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

// -verify-previous must never pass vacuously: it needs -cmd, the discard it
// proves, and a pause trigger, and it rejects every mode that bypasses the
// pause path, both reboot flags included, and the in-place modes whose
// maintained VM can repopulate a punched run during the proof.
func TestVerifyPreviousFlags(t *testing.T) {
	t.Parallel()

	probe := verifyPreviousFlags{enabled: true, cmd: "sha256sum /f", discardSuperseded: true}
	with := func(mutate func(f *verifyPreviousFlags)) verifyPreviousFlags {
		f := probe
		mutate(&f)

		return f
	}

	for _, tc := range []struct {
		name  string
		flags verifyPreviousFlags
		want  string // substring of the error; empty means accepted
	}{
		{name: "off", flags: verifyPreviousFlags{}},
		{name: "off with every other flag", flags: verifyPreviousFlags{cmd: "x", iterations: 3, fphBench: true, shell: true, reboot: true, forceReboot: true, fsOnly: true, inPlace: true, deferMemoryExport: true}},
		{name: "pause", flags: with(func(f *verifyPreviousFlags) { f.pause = true })},
		{name: "cmd-pause", flags: with(func(f *verifyPreviousFlags) { f.cmdPause = "make" })},
		{name: "cmd-signal-pause", flags: with(func(f *verifyPreviousFlags) { f.cmdSignalPause = "make" })},
		{name: "no cmd", flags: with(func(f *verifyPreviousFlags) { f.cmd = ""; f.pause = true }), want: "requires -cmd"},
		{name: "no discard-superseded", flags: with(func(f *verifyPreviousFlags) { f.discardSuperseded = false; f.pause = true }), want: "requires -discard-superseded"},
		{name: "cmd but no pause trigger", flags: probe, want: "requires -pause, -cmd-pause or -cmd-signal-pause"},
		{name: "iterations", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.iterations = 2 }), want: "-iterations"},
		{name: "fph-bench", flags: with(func(f *verifyPreviousFlags) { f.cmdPause = "make"; f.fphBench = true }), want: "-fph-bench"},
		{name: "shell", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.shell = true }), want: "-shell"},
		{name: "reboot", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.reboot = true }), want: "-reboot"},
		{name: "force-reboot", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.forceReboot = true }), want: "-force-reboot"},
		{name: "fs-only", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.fsOnly = true }), want: "-fs-only"},
		{name: "in-place", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.inPlace = true }), want: "-in-place (the maintained VM keeps faulting"},
		{name: "defer-memory-export", flags: with(func(f *verifyPreviousFlags) { f.pause = true; f.deferMemoryExport = true }), want: "-defer-memory-export (it implies -in-place"},
		{name: "defer-memory-export with in-place", flags: with(func(f *verifyPreviousFlags) { f.cmdPause = "make"; f.inPlace = true; f.deferMemoryExport = true }), want: "-defer-memory-export"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := tc.flags.validate()
			if tc.want == "" {
				require.NoError(t, err)

				return
			}
			require.ErrorContains(t, err, tc.want)
		})
	}
}

// The bound on a wait for presence is the handle's own fetch timeout plus a
// margin, derived when the runs are bound, so a slow but working refetch is
// never reported as not present and a chunker with another budget gets its
// own bound rather than the package default's.
func TestPresentBoundDerivesFromTheHandlesFetchTimeout(t *testing.T) {
	t.Parallel()

	for _, timeout := range []time.Duration{7 * time.Second, 90 * time.Second} {
		f := newFakeRefetchDiff(8192)
		f.fetchTimeout = timeout
		bound := boundRuns(f, nil, f.blockSize, nil, 0).presentWait
		require.Equal(t, timeout+presentBoundMargin, bound)
		require.Greater(t, bound, timeout, "the bound outlasts a fetch that completes at its timeout")
	}
}

// fakeRefetchDiff is a diff in its packed storage space with the chunker's
// behaviours the proof must survive: a read of a block the cache does not
// hold streams the whole chunk of chunkBlocks blocks (unless fetchless), and
// the chunk is marked present only when its stream completes: at once, or,
// when delayed, once the test closes publish; a discard that overlaps a
// streaming chunk stales it, so it is never marked present, and a re-read of
// that chunk waits for publish before it streams afresh; the data can end
// mid-block; a discard returns runs to not-present.
type fakeRefetchDiff struct {
	blockSize   int64
	chunkBlocks int64
	data        []byte
	present     map[int64]bool
	// streams holds the chunks a fetch is streaming, by chunk index.
	streams map[int64]*fakeStream
	delayed bool
	publish chan struct{}
	// blocked receives once when a read, a presence query or a fetch wait
	// first waits on a stream the test must release.
	blocked chan struct{}
	// waits counts the reads that waited for a stale stream to terminate;
	// fetchWaits counts the fetch waits entered; peeks counts the presence
	// queries.
	waits      int
	fetchWaits int
	peeks      int
	reads      []int
	readErr    error
	// short, if set, makes every read return one byte fewer than asked.
	short     bool
	fetchless bool
	// fetchTimeout is what the fake reports as its chunker's fetch budget.
	fetchTimeout time.Duration
}

type fakeStream struct {
	stale bool
}

func newFakeRefetchDiff(size int64, presentBlocks ...int64) *fakeRefetchDiff {
	const blockSize = 1024
	f := &fakeRefetchDiff{
		blockSize: blockSize, chunkBlocks: 4, data: make([]byte, size),
		present: map[int64]bool{}, streams: map[int64]*fakeStream{},
		publish: make(chan struct{}), blocked: make(chan struct{}, 1),
		fetchTimeout: time.Second,
	}
	for i := range f.data {
		f.data[i] = byte(i*7 + i/blockSize)
	}
	for _, b := range presentBlocks {
		f.present[b] = true
	}

	return f
}

// blocks returns the block index range covering [off, off+length), clipped
// to the data.
func (f *fakeRefetchDiff) blocks(off, length int64) (int64, int64) {
	end := min(off+length, int64(len(f.data)))

	return off / f.blockSize, (end + f.blockSize - 1) / f.blockSize
}

func (f *fakeRefetchDiff) chunkOf(off int64) int64 {
	return off / (f.blockSize * f.chunkBlocks)
}

// streaming starts, before the proof, a delayed stream of the chunk holding
// off, as a page fault of the baseline probe does.
func (f *fakeRefetchDiff) streaming(off int64) {
	f.delayed = true
	f.streams[f.chunkOf(off)] = &fakeStream{}
}

func (f *fakeRefetchDiff) released() bool {
	select {
	case <-f.publish:
		return true
	default:
		return false
	}
}

// signalBlocked tells the test, once, that the fake waits on its release.
func (f *fakeRefetchDiff) signalBlocked() {
	select {
	case f.blocked <- struct{}{}:
	default:
	}
}

// complete settles every stream once the test released them: a current
// stream marks its chunk present, a stale one terminates without marking.
func (f *fakeRefetchDiff) complete() {
	if !f.released() {
		return
	}
	for c, s := range f.streams {
		if !s.stale {
			f.markChunk(c)
		}
		delete(f.streams, c)
	}
}

func (f *fakeRefetchDiff) markChunk(c int64) {
	_, dataEnd := f.blocks(0, int64(len(f.data)))
	for b := c * f.chunkBlocks; b < min((c+1)*f.chunkBlocks, dataEnd); b++ {
		f.present[b] = true
	}
}

func (f *fakeRefetchDiff) allPresent(off, length int64) bool {
	for b, end := f.blocks(off, length); b < end; b++ {
		if !f.present[b] {
			return false
		}
	}

	return true
}

// stream fetches the chunks covering [off, off+n): a chunk a stale stream
// still holds is waited for first, a chunk already streaming is joined, and
// a fresh stream completes at once unless delayed.
func (f *fakeRefetchDiff) stream(off, n int64) {
	for c := f.chunkOf(off); c <= f.chunkOf(off+n-1); c++ {
		if s := f.streams[c]; s != nil && s.stale {
			f.waits++
			f.signalBlocked()
			<-f.publish
			f.complete()
		}
		switch {
		case !f.delayed:
			f.markChunk(c)
		case f.streams[c] == nil:
			f.streams[c] = &fakeStream{}
		}
	}
}

func (f *fakeRefetchDiff) ReadAt(_ context.Context, p []byte, off int64, _ *storage.FrameTable) (int, error) {
	f.reads = append(f.reads, len(p))
	if f.readErr != nil {
		return 0, f.readErr
	}
	if off >= int64(len(f.data)) {
		return 0, errors.New("read past the end of the diff")
	}
	f.complete()
	n := copy(p, f.data[off:])
	if f.short {
		n--
	}
	if !f.fetchless && n > 0 && !f.allPresent(off, int64(n)) {
		f.stream(off, int64(n))
	}

	return n, nil
}

// WaitFetched waits for the streams of the chunks covering [off, off+length)
// to terminate, which they do only when the test releases them, or for ctx
// to end; a zero-length range covers no chunk.
func (f *fakeRefetchDiff) WaitFetched(ctx context.Context, off, length int64) error {
	f.fetchWaits++
	if length <= 0 {
		return nil
	}
	for c := f.chunkOf(off); c <= f.chunkOf(off+length-1); c++ {
		if f.streams[c] == nil {
			continue
		}
		f.signalBlocked()
		select {
		case <-f.publish:
			f.complete()
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	return nil
}

func (f *fakeRefetchDiff) FetchTimeout() time.Duration {
	return f.fetchTimeout
}

func (f *fakeRefetchDiff) IsCached(_ context.Context, off, length int64) bool {
	f.peeks++
	f.complete()
	for b, end := f.blocks(off, length); b < end; b++ {
		if f.present[b] {
			continue
		}
		if s := f.streams[b/f.chunkBlocks]; s != nil && !s.stale {
			f.signalBlocked()
		}

		return false
	}

	return true
}

// discardFault is what a discard can get wrong, or change, for a test.
type discardFault struct {
	// rewrite changes the bytes a later read of that run returns.
	rewrite *header.BuildMap
	// leavePresent keeps every run present.
	leavePresent bool
	// partial returns only the first block of each run to not-present.
	partial bool
	// delayPresence makes later reads mark presence only once publish is closed.
	delayPresence bool
}

// discard returns the runs to not-present, clipped to the data like the
// cache, stales every stream it overlaps, and reports the bytes present and
// the extent punched.
func (f *fakeRefetchDiff) discard(runs []header.BuildMap, fault discardFault) func() (block.DiscardStats, error) {
	return func() (block.DiscardStats, error) {
		var stats block.DiscardStats
		size := int64(len(f.data))
		f.complete()
		for _, run := range runs {
			start, end := int64(run.Offset), min(int64(run.Offset+run.Length), size)
			if end <= start {
				continue
			}
			for b, last := f.blocks(start, end-start); b < last; b++ {
				if f.present[b] {
					stats.Present += min(f.blockSize, size-b*f.blockSize)
				}
				if fault.leavePresent || (fault.partial && b > start/f.blockSize) {
					continue
				}
				delete(f.present, b)
			}
			for c := f.chunkOf(start); c <= f.chunkOf(end-1); c++ {
				if s := f.streams[c]; s != nil {
					s.stale = true
				}
			}
			stats.Punched += end - start
		}
		if fault.rewrite != nil {
			for i := fault.rewrite.Offset; i < fault.rewrite.Offset+fault.rewrite.Length; i++ {
				f.data[i] ^= 0xff
			}
		}
		if fault.delayPresence {
			f.delayed = true
		}

		return stats, nil
	}
}

// The runs the proof reads are the discard's runs clipped to the diff: a run
// past the end is dropped, one crossing it is cut, and an empty run is
// dropped, since a presence query over it is true at once and it would
// count as present with nothing refetched.
func TestClipRunsDropsEmptyAndPastTheEnd(t *testing.T) {
	t.Parallel()

	runs := []header.BuildMap{
		{Offset: 0, Length: 2048},
		{Offset: 1024, Length: 0},
		{Offset: 7168, Length: 2048},
		{Offset: 8192, Length: 1024},
	}
	require.Equal(t, []header.BuildMap{{Offset: 0, Length: 2048}, {Offset: 7168, Length: 1024}}, clipRuns(runs, 8192))
	require.Empty(t, clipRuns([]header.BuildMap{{Offset: 0, Length: 0}}, 8192), "an empty run alone leaves nothing to read")
}

// The fake's wait matches the chunker's on a zero-length range: it covers no
// chunk, so it returns at once with a stream in flight over its offset.
func TestFakeRefetchDiffZeroLengthWaitReturnsAtOnce(t *testing.T) {
	t.Parallel()

	f := newFakeRefetchDiff(8192)
	f.streaming(0)
	done := make(chan error, 1)
	go func() {
		for _, off := range []int64{0, 1, 1024} {
			if err := f.WaitFetched(t.Context(), off, 0); err != nil {
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
	select {
	case <-f.blocked:
		t.Fatal("the wait blocked on the stream")
	default:
	}
}

// The wait for a re-read run to be marked present ends at its bound: the
// deadline is tested after each poll's sleep, which is clipped to it, so a
// bound shorter than a poll is not stretched to the poll; a run already
// present, or empty, returns at once; a context that ends is seen after the
// sleep; a literal with no sleep set sleeps with time.Sleep. The sleeps are
// asserted as requested, never as wall clock: a loaded host can stretch a
// 1 ms sleep past the poll.
func TestWaitPresentEndsAtTheBound(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	runA := header.BuildMap{Offset: 0, Length: 2048}
	sup := func(f *fakeRefetchDiff, bound time.Duration) supersededRuns {
		s := boundRuns(f, nil, f.blockSize, []header.BuildMap{runA}, int64(len(f.data)))
		s.presentWait = bound

		return s
	}
	// recording returns sup with a sleep that records each requested
	// duration before sleeping it.
	recording := func(s supersededRuns, sleeps *[]time.Duration) supersededRuns {
		s.sleep = func(d time.Duration) {
			*sleeps = append(*sleeps, d)
			time.Sleep(d)
		}

		return s
	}

	t.Run("a run already present returns at once", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192, 0, 1)
		require.NoError(t, waitPresent(ctx, sup(f, time.Second), runA))
		require.Equal(t, 1, f.peeks)
	})

	t.Run("an empty run returns at once", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192)
		require.NoError(t, waitPresent(ctx, sup(f, time.Second), header.BuildMap{Offset: 0, Length: 0}))
		require.Equal(t, 0, f.peeks, "nothing to wait for, so the tracker is not asked")
	})

	t.Run("a bound shorter than a poll ends at the bound, not at the next poll", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192)
		f.fetchless = true
		var sleeps []time.Duration
		start := time.Now()
		err := waitPresent(ctx, recording(sup(f, time.Millisecond), &sleeps), runA)
		elapsed := time.Since(start)
		require.ErrorContains(t, err, "the run is not present 1ms after the re-read")
		require.GreaterOrEqual(t, elapsed, time.Millisecond, "the wait lasts at least the bound")
		require.Equal(t, 1, f.peeks, "the deadline is tested after the sleep, with no poll after it")
		require.Len(t, sleeps, 1)
		require.LessOrEqual(t, sleeps[0], time.Millisecond, "the sleep is clipped to the deadline, not the poll")
	})

	t.Run("a bound of several polls sleeps a poll at a time, clipped to the bound", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192)
		f.fetchless = true
		var sleeps []time.Duration
		start := time.Now()
		err := waitPresent(ctx, recording(sup(f, 25*time.Millisecond), &sleeps), runA)
		elapsed := time.Since(start)
		require.ErrorContains(t, err, "the run is not present 25ms after the re-read")
		require.GreaterOrEqual(t, elapsed, 25*time.Millisecond)
		require.NotEmpty(t, sleeps)
		require.Equal(t, len(sleeps), f.peeks, "one query before each sleep, none after the deadline")
		var total time.Duration
		for _, d := range sleeps {
			require.LessOrEqual(t, d, presentPoll, "no sleep is longer than a poll")
			total += d
		}
		require.LessOrEqual(t, total, 25*time.Millisecond, "the sleeps are clipped to the bound")
	})

	t.Run("a literal with no sleep set sleeps with the default", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192)
		f.fetchless = true
		s := supersededRuns{diff: f, blockSize: f.blockSize, runs: []header.BuildMap{runA}, reads: []header.BuildMap{runA}, presentWait: time.Millisecond}
		var err error
		require.NotPanics(t, func() { err = waitPresent(ctx, s, runA) })
		require.ErrorContains(t, err, "the run is not present 1ms after the re-read")
		require.Equal(t, 1, f.peeks)
	})

	t.Run("a context that ends during a sleep returns its error", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192)
		f.fetchless = true
		ctx, cancel := context.WithCancel(ctx)
		defer cancel()
		s := sup(f, time.Second)
		s.sleep = func(time.Duration) { cancel() }
		require.ErrorIs(t, waitPresent(ctx, s, runA), context.Canceled)
		require.Equal(t, 1, f.peeks, "no query after the context ended")
	})
}

// The proof population is the runs present before the discard; every run is
// hashed through the read path before and after, a block at a time.
func TestVerifyPreviousAfterDiscard(t *testing.T) {
	t.Parallel()

	ctx := t.Context()
	// Three runs over an eight-block diff: A (blocks 0-1) and C (blocks 5-6)
	// are present before the discard, B (block 3) is not.
	runA := header.BuildMap{Offset: 0, Length: 2048}
	runB := header.BuildMap{Offset: 3072, Length: 1024}
	runC := header.BuildMap{Offset: 5120, Length: 2048}
	runs := []header.BuildMap{runA, runB, runC}
	populated := func() *fakeRefetchDiff { return newFakeRefetchDiff(8192, 0, 1, 5, 6) }
	sup := func(f *fakeRefetchDiff) supersededRuns {
		s := boundRuns(f, nil, f.blockSize, runs, int64(len(f.data)))
		s.presentWait = 2 * time.Second

		return s
	}
	probe := func(out string, probed *bool) func() (string, error) {
		return func() (string, error) {
			*probed = true

			return out, nil
		}
	}

	t.Run("the same bytes after the discard pass with the exact figures", func(t *testing.T) {
		t.Parallel()

		f := populated()
		probed := false
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", probe("a", &probed))
		require.NoError(t, out.err)
		require.NoError(t, out.discardErr)
		require.Equal(t, "verify_previous=ok runs=3 refetched_bytes=4096 present_before=2 punched=5120", out.line)
		require.Equal(t, block.DiscardStats{Present: 5120, Punched: 5120}, out.discarded, "B was fetched by the pre-discard read, so the discard found it present too")
		require.True(t, probed, "the guest command is the secondary check after the proof")
		require.Len(t, f.reads, 10, "five blocks read before the discard and five after")
		for _, n := range f.reads {
			require.Equal(t, int(f.blockSize), n, "a run is read one block at a time")
		}
		require.NoError(t, (&runner{discardErr: out.discardErr, verifyErr: out.err}).pauseResult())
	})

	t.Run("a run rounded past the end of the diff is clipped to it", func(t *testing.T) {
		t.Parallel()

		// A packed diff of seven and a half blocks: the last run (blocks
		// 5-7) is rounded to the block size, so its extent ends past the data.
		f := newFakeRefetchDiff(7680, 0, 1, 5, 6, 7)
		tail := header.BuildMap{Offset: 5120, Length: 3072}
		rounded := []header.BuildMap{runA, runB, tail}
		probed := false
		out := verifyPreviousAfterDiscard(ctx, boundRuns(f, nil, f.blockSize, rounded, int64(len(f.data))), f.discard(rounded, discardFault{}), "a", probe("a", &probed))
		require.NoError(t, out.err)
		require.Equal(t, "verify_previous=ok runs=3 refetched_bytes=4608 present_before=2 punched=5632", out.line, "the tail counts its in-file bytes only")
		require.Equal(t, block.DiscardStats{Present: 5632, Punched: 5632}, out.discarded)
		require.Equal(t, []int{1024, 1024, 1024, 1024, 1024, 512, 1024, 1024, 1024, 1024, 1024, 512}, f.reads, "the tail's last read is its in-file half block, before and after")
		require.True(t, probed)
	})

	// The two in-flight cases run in a synctest bubble: the fetch's
	// completion timer and the wait's bound are ordered in virtual time, so
	// the elapsed figures are exact and no real-time race decides them.
	t.Run("a run whose fetch is still in flight when the proof starts is counted once the fetch terminates", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// The baseline probe's page fault started C's chunk fetch and
			// the sandbox close did not wait for it; the fetch completes
			// 300 ms into the proof's wait (three polls of a 100 ms
			// resample, which would have agreed on "absent" at once), so
			// C is in the proof population and the figures are the usual
			// ones.
			f := newFakeRefetchDiff(8192, 0, 1)
			f.streaming(int64(runC.Offset))
			time.AfterFunc(300*time.Millisecond, func() { close(f.publish) })
			start := time.Now()
			out := verifyPreviousAfterDiscard(t.Context(), sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
			require.Equal(t, 300*time.Millisecond, time.Since(start), "the proof waited exactly until C's fetch completed")
			require.NoError(t, out.err)
			require.Equal(t, "verify_previous=ok runs=3 refetched_bytes=4096 present_before=2 punched=5120", out.line)
			require.Equal(t, block.DiscardStats{Present: 5120, Punched: 5120}, out.discarded, "C's fetch completed before the discard; B's chunk was fetched by the pre-discard read")
			require.Equal(t, len(runs), f.fetchWaits, "the proof entered the fetch wait once per run before the sample")
		})
	})

	t.Run("a run whose fetch is still in flight at the bound is left out and the proof skips", func(t *testing.T) {
		t.Parallel()

		synctest.Test(t, func(t *testing.T) {
			// Only C's chunk is streaming and it never completes: the
			// proof waits on the stream until the bound, the sample takes
			// what is there, C is out of the population, nothing else is
			// held, and the run exits non-zero rather than passing on
			// bytes the proof never saw present.
			f := newFakeRefetchDiff(8192)
			f.streaming(int64(runC.Offset))
			s := sup(f)
			s.presentWait = 50 * time.Millisecond
			probed := false
			start := time.Now()
			out := verifyPreviousAfterDiscard(t.Context(), s, f.discard(runs, discardFault{}), "a", probe("a", &probed))
			require.Equal(t, s.presentWait, time.Since(start), "the wait on C's stream held exactly until the bound")
			require.GreaterOrEqual(t, f.fetchWaits, 1, "the proof entered the fetch wait")
			require.ErrorContains(t, out.err, "no superseded run was present before the discard")
			require.Equal(t, "verify_previous=skipped present_bytes=0", out.line)
			require.Equal(t, block.DiscardStats{Present: 0, Punched: 5120}, out.discarded, "C's stream was still in flight at the discard, which staled it")
			require.False(t, probed)
			require.Error(t, (&runner{verifyErr: out.err}).pauseResult(), "the run must exit non-zero")
		})
	})

	t.Run("a run populated by an earlier run's chunk fetch is not present before", func(t *testing.T) {
		t.Parallel()

		// Two absent runs in one four-block chunk: reading the first fetches
		// the chunk, so the second is present by the time it is read, but
		// neither was held before the proof started.
		f := newFakeRefetchDiff(8192)
		pair := []header.BuildMap{{Offset: 0, Length: 1024}, {Offset: 2048, Length: 1024}}
		probed := false
		out := verifyPreviousAfterDiscard(ctx, boundRuns(f, nil, f.blockSize, pair, int64(len(f.data))), f.discard(pair, discardFault{}), "a", probe("a", &probed))
		require.ErrorContains(t, out.err, "no superseded run was present before the discard")
		require.Equal(t, "verify_previous=skipped present_bytes=0", out.line)
		require.Equal(t, block.DiscardStats{Present: 2048, Punched: 2048}, out.discarded, "the pre-discard read fetched both runs' chunk")
		require.False(t, probed)
	})

	t.Run("a discard overlapping a run's stream leaves that run out of the proof population", func(t *testing.T) {
		t.Parallel()

		// Only A is held before the proof; the pre-discard read of B starts
		// its chunk's stream, which is still in flight when the discard
		// stales it, so B is never marked present and its bytes are dropped:
		// the population is A alone, and A's re-read waits for the stale
		// stream to terminate before it streams afresh.
		f := newFakeRefetchDiff(8192, 0, 1)
		f.delayed = true
		go func() {
			<-f.blocked
			close(f.publish)
		}()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
		require.NoError(t, out.err)
		require.Equal(t, "verify_previous=ok runs=3 refetched_bytes=2048 present_before=1 punched=5120", out.line)
		require.Equal(t, block.DiscardStats{Present: 2048, Punched: 5120}, out.discarded, "B's and C's streams had not completed at the discard")
		require.Equal(t, 1, f.waits, "A's re-read waited for the staled stream of its chunk")
		require.Len(t, f.reads, 10)
	})

	t.Run("a run that reads differently after the discard is a mismatch naming it", func(t *testing.T) {
		t.Parallel()

		f := populated()
		probed := false
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{rewrite: &runC}), "a", probe("a", &probed))
		require.ErrorContains(t, out.err, "different bytes after the discard")
		require.Equal(t, "verify_previous=mismatch run=5120/2048", out.line)
		require.False(t, probed, "the proof failed first; the guest command is not consulted")
		require.Error(t, (&runner{verifyErr: out.err}).pauseResult(), "the run must exit non-zero")
	})

	t.Run("no run present before the discard is skipped and fails the run", func(t *testing.T) {
		t.Parallel()

		f := newFakeRefetchDiff(8192)
		probed := false
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", probe("a", &probed))
		require.ErrorContains(t, out.err, "no superseded run was present before the discard")
		require.Equal(t, "verify_previous=skipped present_bytes=0", out.line)
		require.False(t, probed, "nothing to prove, so the guest command is not run")
		require.Equal(t, int64(5120), out.discarded.Punched, "the discard still runs")
		require.Error(t, (&runner{verifyErr: out.err}).pauseResult(), "the run must exit non-zero")
	})

	t.Run("a matching guest command cannot pass a run that reads differently", func(t *testing.T) {
		t.Parallel()

		f := populated()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{rewrite: &runA}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "different bytes after the discard")
		require.Equal(t, "verify_previous=mismatch run=0/2048", out.line)
		require.Error(t, (&runner{verifyErr: out.err}).pauseResult())
	})

	t.Run("a different guest command output still fails after the proof", func(t *testing.T) {
		t.Parallel()

		f := populated()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "b", nil })
		require.ErrorContains(t, out.err, "different -cmd output")
		require.Equal(t, "verify_previous=mismatch probe=cmd", out.line)
	})

	t.Run("a failed guest command fails after the proof", func(t *testing.T) {
		t.Parallel()

		f := populated()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "", errors.New("boot failed") })
		require.ErrorContains(t, out.err, "boot failed")
		require.Equal(t, "verify_previous=error probe=cmd", out.line)
	})

	t.Run("a failed discard skips the proof and fails the run", func(t *testing.T) {
		t.Parallel()

		f := populated()
		probed := false
		out := verifyPreviousAfterDiscard(ctx, sup(f), func() (block.DiscardStats, error) {
			return block.DiscardStats{}, build.ErrDiffStorePinned
		}, "a", probe("a", &probed))
		require.NoError(t, out.err)
		require.ErrorIs(t, out.discardErr, build.ErrDiffStorePinned)
		require.False(t, probed, "a vacuous comparison must not run after a failed discard")
		require.Equal(t, "verify_previous=skipped discard_error="+build.ErrDiffStorePinned.Error(), out.line)
		require.ErrorIs(t, (&runner{discardErr: out.discardErr}).pauseResult(), build.ErrDiffStorePinned, "the run must exit non-zero")
	})

	t.Run("a run still present after the discard is an error before any re-read", func(t *testing.T) {
		t.Parallel()

		f := populated()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{leavePresent: true}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "still present after the discard")
		require.Equal(t, "verify_previous=error run=0/2048", out.line)
		require.Len(t, f.reads, 5, "no run was re-read")
	})

	t.Run("a run with one block still present after the discard is an error", func(t *testing.T) {
		t.Parallel()

		// The run's IsCached is already false with its first block absent;
		// the second block, left behind, would feed the hash.
		f := populated()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{partial: true}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "the block at 1024 is still present after the discard")
		require.Equal(t, "verify_previous=error run=0/2048", out.line)
		require.Len(t, f.reads, 5, "no run was re-read")
	})

	t.Run("a run marked present only after its re-read completed passes", func(t *testing.T) {
		t.Parallel()

		// An uncompressed read returns once its bytes are published; the
		// tracker marks the chunk when the whole fetch completes.
		f := populated()
		go func() {
			<-f.blocked
			close(f.publish)
		}()
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{delayPresence: true}), "a", func() (string, error) { return "a", nil })
		require.NoError(t, out.err)
		require.Equal(t, "verify_previous=ok runs=3 refetched_bytes=4096 present_before=2 punched=5120", out.line)
	})

	t.Run("a run never marked present after the re-read is an error at the bound", func(t *testing.T) {
		t.Parallel()

		f := populated()
		s := sup(f)
		s.presentWait = 50 * time.Millisecond
		out := verifyPreviousAfterDiscard(ctx, s, f.discard(runs, discardFault{delayPresence: true}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "the run is not present 50ms after the re-read")
		require.Equal(t, "verify_previous=error run=0/2048", out.line)
	})

	t.Run("a run not present after the re-read is an error", func(t *testing.T) {
		t.Parallel()

		f := populated()
		f.fetchless = true
		s := sup(f)
		s.presentWait = 0
		out := verifyPreviousAfterDiscard(ctx, s, f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "not present 0s after the re-read")
		require.Equal(t, "verify_previous=error run=0/2048", out.line)
	})

	t.Run("a read failure before the discard still discards and fails the run", func(t *testing.T) {
		t.Parallel()

		f := populated()
		f.readErr = errors.New("upstream gone")
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "upstream gone")
		require.Equal(t, "verify_previous=error", out.line)
		require.Equal(t, int64(5120), out.discarded.Punched, "the discard still runs")
	})

	t.Run("a read failure after the discard names the run", func(t *testing.T) {
		t.Parallel()

		f := populated()
		discard := f.discard(runs, discardFault{})
		out := verifyPreviousAfterDiscard(ctx, sup(f), func() (block.DiscardStats, error) {
			f.readErr = errors.New("upstream gone")

			return discard()
		}, "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "read after the discard")
		require.ErrorContains(t, out.err, "upstream gone")
		require.Equal(t, "verify_previous=error run=0/2048", out.line)
	})

	t.Run("a closed cache during the re-read is reported as closed, never as a mismatch", func(t *testing.T) {
		t.Parallel()

		f := populated()
		discard := f.discard(runs, discardFault{})
		out := verifyPreviousAfterDiscard(ctx, sup(f), func() (block.DiscardStats, error) {
			f.readErr = block.NewErrCacheClosed("memfile")

			return discard()
		}, "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "block cache already closed for path memfile")
		require.Equal(t, "verify_previous=error cache=closed run=0/2048", out.line)
	})

	t.Run("a closed cache before the discard is reported as closed", func(t *testing.T) {
		t.Parallel()

		f := populated()
		f.readErr = block.NewErrCacheClosed("memfile")
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "read before the discard")
		require.Equal(t, "verify_previous=error cache=closed", out.line)
		require.Equal(t, int64(5120), out.discarded.Punched, "the discard still runs")
	})

	t.Run("a discard on a closed cache is reported as closed", func(t *testing.T) {
		t.Parallel()

		f := populated()
		out := verifyPreviousAfterDiscard(ctx, sup(f), func() (block.DiscardStats, error) {
			return block.DiscardStats{}, block.NewErrCacheClosed("memfile")
		}, "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "block cache already closed")
		require.Equal(t, "verify_previous=error cache=closed", out.line)
		require.Error(t, out.discardErr)
		require.Len(t, f.reads, 5, "nothing is re-read")
	})

	t.Run("a short read is an error", func(t *testing.T) {
		t.Parallel()

		f := populated()
		f.short = true
		out := verifyPreviousAfterDiscard(ctx, sup(f), f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "short read 1023 of 1024")
		require.Equal(t, "verify_previous=error", out.line)
	})

	t.Run("a diff that cannot be read directly fails the run", func(t *testing.T) {
		t.Parallel()

		f := populated()
		out := verifyPreviousAfterDiscard(ctx, supersededRuns{blockSize: f.blockSize, runs: runs}, f.discard(runs, discardFault{}), "a", func() (string, error) { return "a", nil })
		require.ErrorContains(t, out.err, "cannot be read directly")
		require.Equal(t, "verify_previous=error", out.line)
	})

	t.Run("nothing superseded is skipped and fails the run", func(t *testing.T) {
		t.Parallel()

		f := populated()
		probed := false
		out := verifyPreviousAfterDiscard(ctx, supersededRuns{diff: f, blockSize: f.blockSize}, func() (block.DiscardStats, error) {
			return block.DiscardStats{}, nil
		}, "a", probe("a", &probed))
		require.ErrorContains(t, out.err, "no superseded run was present")
		require.Equal(t, "verify_previous=skipped present_bytes=0", out.line)
		require.False(t, probed)
		require.Empty(t, f.reads)
	})
}
