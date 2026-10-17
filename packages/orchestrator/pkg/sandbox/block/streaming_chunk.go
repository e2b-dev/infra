//go:build linux

package block

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/block/metrics"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	// defaultFetchTimeout is the maximum time a single 4MB chunk fetch may take.
	// Acts as a safety net: if the upstream hangs, the goroutine won't live forever.
	defaultFetchTimeout = 60 * time.Second
)

type Chunker struct {
	cache        *Cache
	metrics      metrics.Metrics
	fetchTimeout time.Duration
	featureFlags *featureflags.Client
	objType      storage.SeekableObjectType

	size int64

	fetchMu       sync.Mutex
	fetchSessions []*fetchSession
	// discards is a ring of the newest discards, under fetchMu; discardEvicted
	// is the generation of the newest one pushed out of it.
	discards       []discardRecord
	discardNext    int
	discardEvicted uint64

	// testHooks is nil outside tests; its callbacks force interleavings a
	// scheduler would not reproduce.
	testHooks *chunkerTestHooks
}

// discardRingSize bounds the discards a chunker remembers; a session older
// than the oldest remembered one cannot be proven current and counts as stale.
const discardRingSize = 256

// discardRecord is one DiscardRuns call: one generation for all its runs.
type discardRecord struct {
	gen  uint64
	runs []Range
}

func (d *discardRecord) overlaps(off, end int64) bool {
	for _, r := range d.runs {
		if r.Start < end && off < r.End() {
			return true
		}
	}

	return false
}

type chunkerTestHooks struct {
	// beforeCopy runs in ReadAt between the readiness proof and the copy.
	beforeCopy func(off, length int64)
	// beforeBatch runs in progressiveFetch after the staleness check and before
	// each batch read; written is the byte count streamed so far.
	beforeBatch func(s *fetchSession, written int64)
	// sessionCreated runs under fetchMu once a replacement or fresh session is listed.
	sessionCreated func(s *fetchSession)
	// waitingForWriter runs before a reader waits for an overlapping streaming session.
	waitingForWriter func(streaming *fetchSession)
	// duringDiscard runs inside DiscardRuns while the cache write lock is held.
	duringDiscard func()
	// bufferTaken runs in progressiveFetch once s took its private buffer.
	bufferTaken func(s *fetchSession)
}

func NewChunker(
	ff *featureflags.Client,
	size, blockSize int64,
	cachePath string,
	metrics metrics.Metrics,
	objType storage.SeekableObjectType,
) (*Chunker, error) {
	cache, err := NewCache(size, blockSize, cachePath, false)
	if err != nil {
		return nil, fmt.Errorf("failed to create file cache: %w", err)
	}
	cache.refetchable = true

	return &Chunker{
		size:         size,
		cache:        cache,
		metrics:      metrics,
		featureFlags: ff,
		fetchTimeout: defaultFetchTimeout,
		objType:      objType,
	}, nil
}

// ReadAt and Slice take {upstream, ft} as a paired snapshot from the caller.
// The caller is responsible for keeping them consistent.
func (c *Chunker) ReadAt(ctx context.Context, b []byte, off int64, upstream storage.RangeOpener, ft *storage.FrameTable) (int, error) {
	ct := ft.CompressionType()
	readStart := time.Now()
	for {
		gen := c.cache.discardGen.Load()
		src, err := c.ensureCached(ctx, off, int64(len(b)), upstream, ft)
		if err != nil {
			c.metrics.ChunkSliceTimerFactory.Record(ctx, time.Since(readStart), 0, storage.ErrAttrs(c.objType, src, ct, err))

			return 0, err
		}
		if h := c.testHooks; h != nil && h.beforeCopy != nil {
			h.beforeCopy(off, int64(len(b)))
		}

		n, err := c.cache.readAtDirect(ctx, b, off)
		if err != nil {
			c.metrics.ChunkSliceTimerFactory.Record(ctx, time.Since(readStart), int64(n), storage.ErrAttrs(c.objType, src, ct, err))

			return n, fmt.Errorf("failed to read from cache at offset %d: %w", off, err)
		}
		// A discard between the readiness proof and the copy may have punched
		// what was copied; one elsewhere in the file has not.
		if c.cache.discardGen.Load() != gen && c.discardedSince(gen, off, off+int64(len(b))) {
			continue
		}
		c.metrics.ChunkSliceTimerFactory.Record(ctx, time.Since(readStart), int64(n), storage.OKAttrs(c.objType, src, ct))

		return n, nil
	}
}

func (c *Chunker) Slice(ctx context.Context, off, length int64, upstream storage.RangeOpener, ft *storage.FrameTable) ([]byte, error) {
	b := make([]byte, length)
	n, err := c.ReadAt(ctx, b, off, upstream, ft)
	if err != nil {
		return nil, err
	}

	return b[:n], nil
}

// ensureCached fetches every chunk touched by [off, off+length). The returned
// source attributes the work that populated the cache; SourceMmap means the
// full range was already resident.
func (c *Chunker) ensureCached(ctx context.Context, off, length int64, upstream storage.RangeOpener, ft *storage.FrameTable) (storage.Source, error) {
	if c.cache.isCached(off, length) {
		return storage.SourceMmap, nil
	}

	var src storage.Source
	end := off + length
	for cur := off; cur < end; {
		chunkOff, chunkLen, lerr := c.locateChunk(cur, ft)
		if lerr != nil {
			return src, fmt.Errorf("failed to locate chunk for offset %d: %w", cur, lerr)
		}
		chunkEnd := chunkOff + chunkLen
		rangeEnd := min(end, chunkEnd)
		s, err := c.fetch(ctx, cur, rangeEnd-cur, upstream, ft)
		if err != nil {
			return s, fmt.Errorf("failed to ensure data at %d-%d: %w", cur, rangeEnd, err)
		}
		src = max(src, s)
		cur = chunkEnd
	}

	return src, nil
}

// getOrCreateSession returns a fetch session for the chunk at [off, off+length),
// or (nil, true, nil) if the data is already fully cached. A session that
// cannot be joined (stale, or framed on another geometry) but is still
// streaming an overlapping range must terminate before a replacement is
// created, so at most one writer touches a byte at any time; the wait is
// bounded by the caller's context and that fetch's own timeout.
func (c *Chunker) getOrCreateSession(ctx context.Context, off, length int64, upstream storage.RangeOpener, ft *storage.FrameTable) (_ *fetchSession, cached bool, _ error) {
	for {
		s, cached, streaming := c.joinOrCreateSession(ctx, off, length, upstream, ft)
		if streaming == nil {
			return s, cached, nil
		}
		if h := c.testHooks; h != nil && h.waitingForWriter != nil {
			h.waitingForWriter(streaming)
		}
		if err := streaming.waitTerminated(ctx); err != nil {
			return nil, false, err
		}
	}
}

// joinOrCreateSession is one pass of getOrCreateSession under fetchMu; a
// non-nil streaming is an unjoinable session still writing an overlapping range.
func (c *Chunker) joinOrCreateSession(ctx context.Context, off, length int64, upstream storage.RangeOpener, ft *storage.FrameTable) (_ *fetchSession, cached bool, streaming *fetchSession) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	for _, s := range c.fetchSessions {
		if !c.staleLocked(s) && s.contains(off, length) {
			return s, false, nil
		}
		if s.overlaps(off, length) && !s.isTerminated() {
			return nil, false, s
		}
	}

	// Re-check cache under fetchMu. A fetch can finish (marking blocks
	// cached via setIsCached) and remove itself from sessions between
	// the lock-free Slice() and the session scan above. The lock
	// provides a happens-before guarantee that the bitmap writes are visible.
	if c.cache.isCached(off, length) {
		return nil, true, nil
	}

	// Detach from the caller's cancel signal so the shared fetch goroutine
	// continues even if the first caller's context is cancelled. Trace/value
	// context is preserved for metrics. The (upstream, ft) pair is captured
	// by value here — in-flight sessions are unaffected by later swaps.
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), c.fetchTimeout)
	s := newFetchSession(off, length, c.cache)
	s.cancel = cancel
	c.fetchSessions = append(c.fetchSessions, s)
	if h := c.testHooks; h != nil && h.sessionCreated != nil {
		h.sessionCreated(s)
	}

	go c.runFetch(fetchCtx, s, upstream, ft)

	return s, false, nil
}

// fetch ensures the chunk for [off, off+length) is fetched and waits
// for every block the range spans (a span can cross block boundaries
// after dedup; waiting only on the start block leaves the tail unfetched).
func (c *Chunker) fetch(ctx context.Context, off, length int64, upstream storage.RangeOpener, ft *storage.FrameTable) (storage.Source, error) {
	// (upstream, ft) is a per-call snapshot, so this read can be served by a
	// session created before a peer→storage switch and framed on a different
	// geometry (4MB uncompressed peer chunks vs smaller compressed frames).
	// bytesReady counts from the session's chunkOff, so all readiness math
	// below is in the session's frame.
	chunkOff, chunkLen, err := c.locateChunk(off, ft)
	if err != nil {
		return storage.UnknownSource, fmt.Errorf("failed to locate chunk for offset %d: %w", off, err)
	}

	blockSize := c.cache.BlockSize()
	startBlock := (off / blockSize) * blockSize
	endBlock := ((off + length - 1) / blockSize) * blockSize

	// A session that went stale under a discard terminates with errFetchStale
	// instead of vouching for punched bytes; its waiters fetch again.
	for {
		session, justGotCached, err := c.getOrCreateSession(ctx, chunkOff, chunkLen, upstream, ft)
		if err != nil {
			return storage.UnknownSource, err
		}
		if justGotCached {
			return storage.SourceMmap, nil
		}

		chunkEnd := session.chunkOff + session.chunkLen

		// Already streamed past every byte we need: it's in the mmap, source=mmap.
		endByte := min(endBlock+blockSize, chunkEnd) - session.chunkOff
		if session.bytesReady.Load() >= endByte {
			return storage.SourceMmap, nil
		}

		stale := false
		for b := startBlock; b <= endBlock; b += blockSize {
			if b >= chunkEnd {
				break // tail belongs to the caller's next chunk fetch.
			}
			err := session.registerAndWait(ctx, b)
			if errors.Is(err, errFetchStale) {
				stale = true

				break
			}
			if err != nil {
				return session.Source(), err
			}
		}
		if !stale {
			return session.Source(), nil
		}
	}
}

// runFetch fetches data from storage into the mmap cache. Runs in a background goroutine.
func (c *Chunker) runFetch(ctx context.Context, s *fetchSession, upstream storage.RangeOpener, ft *storage.FrameTable) {
	defer s.cancel()

	ctx, span := tracer.Start(ctx, "chunk.fetch")
	defer span.End()
	span.SetAttributes(
		attribute.Int64("off", s.chunkOff),
		attribute.Int64("len", s.chunkLen),
	)

	defer c.releaseSession(s)

	// Unconditionally terminate the session on exit so registerAndWait
	// never blocks forever — whether the fetch succeeded, failed, or panicked.
	defer func() {
		if r := recover(); r != nil {
			s.failIfRunning(fmt.Errorf("fetch panicked: %v", r))

			return
		}

		// Safety net: if no code path called setDone/fail, terminate now.
		s.failIfRunning(errors.New("fetch exited without completing"))
	}()

	releaseStream, err := c.cache.holdStream()
	if err != nil {
		s.fail(err)

		return
	}
	defer releaseStream()

	ct := ft.CompressionType()

	fetchStart := time.Now()

	res, frame, err := c.progressiveFetch(ctx, s, upstream, ft)
	if frame != nil {
		defer frameBuffers.Put(frame)
	}
	var readBytes int64
	if res.stats != nil {
		readBytes = res.stats.DeliveredBytes
	}
	// A compressed frame reaches the mapping only once its checksum verified
	// on Close; a failed frame leaves every block as it was.
	if err == nil && frame != nil {
		err = c.publish(s, s.chunkOff, *frame)
	}
	if err != nil {
		// A stream cancelled by a discard terminates stale, so its waiters
		// fetch again instead of failing. Transports report the cancellation
		// in their own shape (a status, a cut body), so the verdict is the
		// session's own state: stale, with its fetch context done.
		if errors.Is(err, errFetchStale) || (ctx.Err() != nil && c.isStale(s)) {
			err = errFetchStale
		}
		storage.RecordReadFetch(ctx, time.Since(fetchStart), readBytes, storage.ErrAttrs(c.objType, res.source, ct, err))

		s.fail(err)

		return
	}

	// Mark entire chunk as cached BEFORE releasing waiters.
	// This ensures isCached returns true before the session is removed from fetchSessions,
	// closing the TOCTOU window in getOrCreateSession.
	marked := c.markFetched(s)

	fetchDuration := time.Since(fetchStart)
	storage.RecordReadFetch(ctx, fetchDuration, readBytes, storage.OKAttrs(c.objType, res.source, ct))

	// fetch wall / (open + read + decompress); >1 = unaccounted overhead.
	if res.stats != nil {
		if work := res.openDuration + res.stats.Read + res.stats.Decompress; work > 0 {
			ratio := fetchDuration.Seconds() / work.Seconds()
			storage.RecordPipelineEfficiency(ctx, ratio, storage.OKAttrs(c.objType, res.source, ct))
		}
	}

	if !marked {
		s.fail(errFetchStale)

		return
	}

	s.setDone()
}

// fetchStats is one progressiveFetch outcome: the resolved source, the open/TTFB
// wall, and the read sub-stage stats (nil if the open failed).
type fetchStats struct {
	source       storage.Source
	openDuration time.Duration
	stats        *storage.ReadStats
}

// progressiveFetch streams the session's chunk from upstream. The reader never
// sees the mapping: an uncompressed batch is read into a private buffer and
// published after the staleness check, so a punch landing mid-read cannot
// reach a writeback capture; a compressed frame is decoded into a private
// buffer, returned for the caller to publish once Close verified it.
func (c *Chunker) progressiveFetch(ctx context.Context, s *fetchSession, upstream storage.RangeOpener, ft *storage.FrameTable) (res fetchStats, frame *[]byte, err error) {
	openStart := time.Now()
	reader, source, err := upstream.OpenRangeReader(ctx, s.chunkOff, s.chunkLen, ft)
	res.source = source
	res.openDuration = time.Since(openStart)
	s.setSource(source)
	if err != nil {
		return res, nil, fmt.Errorf("failed to open range reader at %d: %w", s.chunkOff, err)
	}

	defer func() {
		var closeErr error
		res.stats, closeErr = reader.Close(context.WithoutCancel(ctx))

		// A compressed frame's CRC is only verified once its footer is consumed
		// on Close, so a Close error is a verification failure that must fail
		// the fetch (don't cache or release a corrupt frame). A read error, if
		// any, takes precedence.
		if err == nil {
			err = closeErr
		}

		ct := ft.CompressionType()
		attrs := storage.OKAttrs(c.objType, source, ct)
		if err != nil {
			attrs = storage.ErrAttrs(c.objType, source, ct, err)
		}

		var readDur time.Duration
		var readBytes int64
		if res.stats != nil {
			readDur, readBytes = res.stats.Read, res.stats.StoredBytes
		}
		storage.RecordReadRead(ctx, readDur, readBytes, attrs)
	}()

	blockSize := c.cache.BlockSize()
	readBatch := max(blockSize, int64(c.featureFlags.IntFlag(ctx, featureflags.MinChunkerReadSizeKB))*1024)

	compressed := ft.IsCompressed()
	var buf, batch *[]byte
	defer func() {
		if batch != nil {
			batchBuffers.Put(batch)
		}
	}()

	var totalRead int64
	for totalRead < s.chunkLen {
		// A session whose chunk was discarded stops before its next batch; the
		// batch in flight is never published, so a punched hole is not refilled.
		if c.isStale(s) {
			return res, frame, errFetchStale
		}
		// The private buffer is taken only once the session proved current, so
		// a session stale before its first batch allocates nothing.
		if buf == nil {
			if compressed {
				frame = getBuffer(&frameBuffers, s.chunkLen)
				buf = frame
			} else {
				batch = getBuffer(&batchBuffers, min(readBatch, s.chunkLen))
				buf = batch
			}
			if h := c.testHooks; h != nil && h.bufferTaken != nil {
				h.bufferTaken(s)
			}
		}
		if h := c.testHooks; h != nil && h.beforeBatch != nil {
			h.beforeBatch(s, totalRead)
		}

		// Read in batches of max(blockSize, minReadBatchSize) to align notification
		// granularity with the read size and minimize lock/notify overhead.
		readEnd := min(totalRead+readBatch, s.chunkLen)
		var dst []byte
		if compressed {
			dst = (*buf)[totalRead:readEnd]
		} else {
			dst = (*buf)[:readEnd-totalRead]
		}
		n, readErr := io.ReadFull(reader, dst)

		if n > 0 && !compressed {
			// Dirty marking is deferred to runFetch after the full chunk is fetched.
			// With coarse dirty granularity, marking here would expose partially-written data.
			//
			// Compressed chunks are a single frame whose CRC is only verified
			// once the footer is consumed on Close. Releasing waiters here
			// would hand out bytes that a later CRC failure proves corrupt, so
			// their release is deferred to setDone after verification.
			//
			// A stale refusal decides only whether the bytes are published; a
			// read that failed reports its own error below.
			perr := c.publish(s, s.chunkOff+totalRead, dst[:n])
			if perr != nil && readErr == nil {
				return res, frame, perr
			}
			if perr == nil {
				s.advance(totalRead + int64(n))
			}
		}
		totalRead += int64(n)

		if readErr != nil {
			if totalRead >= s.chunkLen {
				break // all bytes received; trailing EOF is expected
			}

			return res, frame, fmt.Errorf("failed reading at offset %d after %d bytes: %w", s.chunkOff, totalRead, readErr)
		}
	}

	return res, frame, nil
}

// batchBuffers and frameBuffers pool the private buffers a fetch reads into:
// one batch of an uncompressed stream, or one whole decoded frame. One pool
// per capacity class, so the two sizes never trade places.
var batchBuffers, frameBuffers sync.Pool

// bufferPool is what getBuffer needs of a sync.Pool.
type bufferPool interface {
	Get() any
	Put(x any)
}

// getBuffer pops a buffer of at least n bytes from pool or allocates one. A
// pooled buffer too small for n (a short last chunk's) is replaced, never
// returned: re-inserted, it would be popped for every later full request and
// defeat the pool.
func getBuffer(pool bufferPool, n int64) *[]byte {
	if v := pool.Get(); v != nil {
		b := v.(*[]byte)
		if int64(cap(*b)) >= n {
			*b = (*b)[:n]

			return b
		}
	}
	b := make([]byte, n)

	return &b
}

// publish copies buf into the mapping at off under the cache read lock, after
// proving s current: a punch is write-locked, so it lands wholly before (and
// stales s) or wholly after this copy. Blocks the tracker already holds as
// present are never written; only the blocks this session is responsible for.
func (c *Chunker) publish(s *fetchSession, off int64, buf []byte) error {
	c.cache.mu.RLock()
	defer c.cache.mu.RUnlock()

	if c.isStale(s) {
		return errFetchStale
	}

	return c.cache.writeAbsentLocked(buf, off)
}

// releaseSession removes s from the active list (swap-delete).
func (c *Chunker) releaseSession(s *fetchSession) {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	for i, a := range c.fetchSessions {
		if a == s {
			c.fetchSessions[i] = c.fetchSessions[len(c.fetchSessions)-1]
			c.fetchSessions[len(c.fetchSessions)-1] = nil
			c.fetchSessions = c.fetchSessions[:len(c.fetchSessions)-1]

			return
		}
	}
}

// markFetched records s's chunk as present unless a discard landed since s was
// created: its bytes may be punched, so they stay not-present and refetch. The
// cache read lock orders the mark wholly before a punch or wholly after its
// generation bump, so a punched chunk is never marked present.
func (c *Chunker) markFetched(s *fetchSession) bool {
	c.cache.mu.RLock()
	defer c.cache.mu.RUnlock()
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	if c.staleLocked(s) {
		return false
	}
	c.cache.setIsCached(s.chunkOff, s.chunkLen)

	return true
}

func (c *Chunker) isStale(s *fetchSession) bool {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	return c.staleLocked(s)
}

// staleLocked reports whether a discard newer than s's stamp overlaps its
// chunk, or is too old for the ring to tell. Staleness is monotonic, so the
// verdict is kept on the session once reached. Must be called with fetchMu held.
func (c *Chunker) staleLocked(s *fetchSession) bool {
	if s.stale {
		return true
	}
	if s.gen < c.discardEvicted || c.discardedSinceLocked(s.gen, s.chunkOff, s.chunkOff+s.chunkLen) {
		s.stale = true

		return true
	}

	return false
}

// discardedSince reports whether a discard newer than gen overlaps [off, end),
// or gen is too old for the ring to tell.
func (c *Chunker) discardedSince(gen uint64, off, end int64) bool {
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	return gen < c.discardEvicted || c.discardedSinceLocked(gen, off, end)
}

// discardedSinceLocked reports whether a recorded discard newer than gen
// overlaps [off, end). The ring is walked newest-first and the walk stops at
// the first record at or below gen. Must be called with fetchMu held.
func (c *Chunker) discardedSinceLocked(gen uint64, off, end int64) bool {
	n := len(c.discards)
	newest := n - 1
	if n == discardRingSize {
		newest = (c.discardNext + n - 1) % n
	}
	for k := range n {
		d := &c.discards[(newest-k+n)%n]
		if d.gen <= gen {
			return false
		}
		if d.overlaps(off, end) {
			return true
		}
	}

	return false
}

// recordDiscardLocked stamps runs with the next generation and cancels every
// listed session they overlap: the per-batch check is the write barrier, the
// cancellation is what stops a stream blocked on its upstream. Must be called
// with fetchMu held.
func (c *Chunker) recordDiscardLocked(runs []Range) {
	d := discardRecord{gen: c.cache.discardGen.Add(1), runs: runs}
	for _, s := range c.fetchSessions {
		if !d.overlaps(s.chunkOff, s.chunkOff+s.chunkLen) {
			continue
		}
		s.stale = true
		s.cancel()
	}
	if len(c.discards) < discardRingSize {
		c.discards = append(c.discards, d)

		return
	}
	c.discardEvicted = c.discards[c.discardNext].gen
	c.discards[c.discardNext] = d
	c.discardNext = (c.discardNext + 1) % discardRingSize
}

// DiscardRuns forgets every run so the next read refetches it from upstream,
// reporting the bytes that were present and the extent punched; see
// Cache.discardLocked for the argument rules (the runs need not be sorted or
// disjoint). One generation and one record serve the whole call, however
// many runs it carries. The punch runs under the cache write lock; the session
// lock is taken only to record the discard, so a session on a run is stamped
// either before the record (stale: it can never vouch for the punched bytes)
// or after it. Fetches stream under streamMu, so one blocked on its upstream
// never delays a discard.
func (c *Chunker) DiscardRuns(runs []Range) (DiscardStats, error) {
	c.cache.mu.Lock()
	defer c.cache.mu.Unlock()

	if h := c.testHooks; h != nil && h.duringDiscard != nil {
		h.duringDiscard()
	}
	stats, clipped, err := c.cache.discardLocked(runs)
	if err != nil {
		return DiscardStats{}, err
	}

	if len(clipped) > 0 {
		c.fetchMu.Lock()
		c.recordDiscardLocked(clipped)
		c.fetchMu.Unlock()
	}

	return stats, nil
}

// Discard is DiscardRuns for the single run [off, off+length).
func (c *Chunker) Discard(off, length int64) (DiscardStats, error) {
	return c.DiscardRuns([]Range{{Start: off, Size: length}})
}

// locateChunk returns the aligned (offset, length) of the chunk containing off.
// For compressed data the frame table defines chunk boundaries; for
// uncompressed data chunks are MemoryChunkSize-aligned (for backwards
// compatibility) and clamped to file size.
func (c *Chunker) locateChunk(off int64, ft *storage.FrameTable) (chunkOff, chunkLen int64, err error) {
	if ft.IsCompressed() {
		r, err := ft.LocateUncompressed(off)
		if err != nil {
			return 0, 0, err
		}

		return r.Offset, int64(r.Length), nil
	}

	chunkOff = (off / storage.MemoryChunkSize) * storage.MemoryChunkSize

	return chunkOff, min(int64(storage.MemoryChunkSize), c.size-chunkOff), nil
}

func (c *Chunker) Close() error {
	return c.cache.Close()
}

// IsCached reports whether [off, off+length) is fully present in the local
// mmap cache (no remote fetch needed). Used by best-effort dedup.
func (c *Chunker) IsCached(_ context.Context, off, length int64) bool {
	return c.cache.isCached(off, length)
}

// FetchTimeout is this chunker's bound on a single chunk fetch; a chunk is
// marked present only when its whole fetch completes within it.
func (c *Chunker) FetchTimeout() time.Duration {
	return c.fetchTimeout
}

// WaitFetched waits for the fetch sessions overlapping [off, off+length) to
// terminate and returns nil once a scan of the session list finds none
// running, or the context's error if it ends first. A session terminates
// completed (its chunk marked present first), stale (a discard overlapped
// it, or its mark was refused as stale; the chunk is left absent) or failed
// (an upstream error or the fetch timeout; the chunk is left absent and not
// stale), so nil does not mean the range is present: a caller re-queries
// presence. Each scan covers the sessions listed at that moment: a reader
// blocked on an unjoinable stale session lists its replacement only after
// that session terminates, so a fetch can start right after the scan that
// returned nil. A caller that needs quiescence must hold off new readers
// itself. A zero-length range overlaps nothing and returns at once.
func (c *Chunker) WaitFetched(ctx context.Context, off, length int64) error {
	for {
		s := c.runningSession(off, length)
		if s == nil {
			return nil
		}
		if err := s.waitTerminated(ctx); err != nil {
			return err
		}
	}
}

// runningSession is a listed session overlapping [off, off+length) that has
// not terminated, or nil; a zero-length range overlaps nothing.
func (c *Chunker) runningSession(off, length int64) *fetchSession {
	if length <= 0 {
		return nil
	}
	c.fetchMu.Lock()
	defer c.fetchMu.Unlock()

	for _, s := range c.fetchSessions {
		if s.overlaps(off, length) && !s.isTerminated() {
			return s
		}
	}

	return nil
}

func (c *Chunker) Size() int64 {
	return c.size
}

func (c *Chunker) FileSize(ctx context.Context) (int64, error) {
	return c.cache.FileSize(ctx)
}
