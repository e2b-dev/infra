// Demo test: runs the production compressStream from this branch
// (per-goroutine buffer release) side-by-side with a verbatim mirror of
// the PR #2863 design (frame.input + releaseInputBuffers checkpoint).
//
// Both sides see the same input, same CompressConfig, same uploader, same
// real zstd compressor pool. We report runtime memstats deltas so the
// buffer-lifecycle difference shows up as a concrete byte count.
//
// Illustration file — delete before merging the PR.
package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

// ============================================================================
// Verbatim mirror of compress_upload.go from PR #2863 (commit ef2d92fa1).
// Renamed with `old` prefix to coexist with the production code in this branch.
// Only behavioral changes: none. Only syntactic: identifier renames.
// ============================================================================

type oldFrame struct {
	uncompressedSize int
	compressed       []byte
	input            *[]byte
}

type oldPart struct {
	index          int
	frames         []*oldFrame
	compressedSize atomic.Int64
	compress       *errgroup.Group
	inputPool      *sync.Pool
}

func oldNewPart(index int, parentCtx context.Context, workers int, inputPool *sync.Pool) (*oldPart, context.Context) {
	p := &oldPart{index: index, inputPool: inputPool}
	var ctx context.Context
	p.compress, ctx = errgroup.WithContext(parentCtx)
	p.compress.SetLimit(workers)

	return p, ctx
}

func (p *oldPart) addFrame(ctx context.Context, uncompressedData []byte, pool *sync.Pool) {
	frameInPart := &oldFrame{uncompressedSize: len(uncompressedData)}
	p.frames = append(p.frames, frameInPart)

	p.compress.Go(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := pool.Get().(compressor)
		out, err := c.compress(uncompressedData)
		pool.Put(c)
		if err != nil {
			return err
		}
		frameInPart.compressed = out
		p.compressedSize.Add(int64(len(out)))

		return nil
	})
}

func (p *oldPart) releaseInputBuffers() {
	for _, f := range p.frames {
		if f.input != nil {
			p.inputPool.Put(f.input)
			f.input = nil
		}
	}
}

func oldCompressStream(ctx context.Context, in io.Reader, cfg CompressConfig, uploader partUploader, maxUploadConcurrency int, sink FrameSink) (*FrameTable, [32]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("start upload: %w", err)
	}
	defer uploader.Close()

	if maxUploadConcurrency < 1 {
		maxUploadConcurrency = 1
	}
	work, workCtx := errgroup.WithContext(ctx)
	work.SetLimit(maxUploadConcurrency + 1)

	q := make(chan *oldPart, 2)
	hasher := sha256.New()
	work.Go(func() error {
		defer close(q)

		return oldReadLoop(workCtx, in, cfg, hasher, q)
	})

	var frameSizes []FrameSize
	var cOffset int64
	var loopErr error
	for p := range q {
		err := p.compress.Wait()
		p.releaseInputBuffers()
		if err != nil {
			loopErr = fmt.Errorf("compress frames: %w", err)
			cancel()

			break
		}

		var compressed [][]byte
		for _, f := range p.frames {
			frameSizes = append(frameSizes, FrameSize{U: int32(f.uncompressedSize), C: int32(len(f.compressed))})
			compressed = append(compressed, f.compressed)
			if sink != nil {
				sink(ctx, cOffset, f.compressed)
			}
			cOffset += int64(len(f.compressed))
		}

		pi := p.index
		work.Go(func() error {
			return uploader.UploadPart(workCtx, pi, compressed...)
		})
	}

	for range q { //nolint:revive // intentional drain
	}
	workErr := work.Wait()

	if err := errors.Join(loopErr, workErr); err != nil {
		return nil, [32]byte{}, err
	}

	if err := uploader.Complete(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("complete upload: %w", err)
	}

	ft := NewFrameTable(cfg.CompressionType(), frameSizes)

	return ft, sum256(hasher), nil
}

func oldReadLoop(ctx context.Context, in io.Reader, cfg CompressConfig, hasher io.Writer, q chan<- *oldPart) error {
	compressors, err := newCompressorPool(cfg)
	if err != nil {
		return err
	}

	frameSize := cfg.FrameSize()
	minPartSize := cfg.MinPartSize()
	workers := max(cfg.FrameEncodeWorkers, 1)
	inputPool := &sync.Pool{
		New: func() any {
			buf := make([]byte, frameSize)

			return &buf
		},
	}
	p, compressCtx := oldNewPart(1, ctx, workers, inputPool)

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		bufPtr := inputPool.Get().(*[]byte)
		buf := (*bufPtr)[:frameSize]
		n, err := io.ReadFull(in, buf)

		eof := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if err != nil && !eof {
			inputPool.Put(bufPtr)

			return fmt.Errorf("read frame: %w", err)
		}

		if n > 0 {
			hasher.Write(buf[:n])
			p.addFrame(compressCtx, buf[:n], compressors)
			p.frames[len(p.frames)-1].input = bufPtr
		} else {
			inputPool.Put(bufPtr)
		}

		if eof {
			if len(p.frames) > 0 {
				select {
				case q <- p:
				case <-ctx.Done():
					p.releaseInputBuffers()

					return ctx.Err()
				}
			}

			return nil
		}

		if p.compressedSize.Load() >= minPartSize {
			select {
			case q <- p:
			case <-ctx.Done():
				p.releaseInputBuffers()

				return ctx.Err()
			}
			p, compressCtx = oldNewPart(p.index+1, ctx, workers, inputPool)
		}
	}
}

// ============================================================================
// MAIN form — pre-PR #2863 design. No input pool at all: each frame allocates
// a fresh `make([]byte, frameSize)` per iteration and relies on GC.
// Mirrors compress_upload.go from immediately before commit 5b4ac5378.
// ============================================================================

type mainFrame struct {
	uncompressedSize int
	compressed       []byte
}

type mainPart struct {
	index          int
	frames         []*mainFrame
	compressedSize atomic.Int64
	compress       *errgroup.Group
}

func mainNewPart(index int, parentCtx context.Context, workers int) (*mainPart, context.Context) {
	p := &mainPart{index: index}
	var ctx context.Context
	p.compress, ctx = errgroup.WithContext(parentCtx)
	p.compress.SetLimit(workers)

	return p, ctx
}

func (p *mainPart) addFrame(ctx context.Context, uncompressedData []byte, pool *sync.Pool) {
	frameInPart := &mainFrame{uncompressedSize: len(uncompressedData)}
	p.frames = append(p.frames, frameInPart)
	p.compress.Go(func() error {
		if err := ctx.Err(); err != nil {
			return err
		}
		c := pool.Get().(compressor)
		out, err := c.compress(uncompressedData)
		pool.Put(c)
		if err != nil {
			return err
		}
		frameInPart.compressed = out
		p.compressedSize.Add(int64(len(out)))

		return nil
	})
}

func mainCompressStream(ctx context.Context, in io.Reader, cfg CompressConfig, uploader partUploader, maxUploadConcurrency int, sink FrameSink) (*FrameTable, [32]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("start upload: %w", err)
	}
	defer uploader.Close()
	if maxUploadConcurrency < 1 {
		maxUploadConcurrency = 1
	}
	work, workCtx := errgroup.WithContext(ctx)
	work.SetLimit(maxUploadConcurrency + 1)
	q := make(chan *mainPart, 2)
	hasher := sha256.New()
	work.Go(func() error {
		defer close(q)

		return mainReadLoop(workCtx, in, cfg, hasher, q)
	})
	var frameSizes []FrameSize
	var cOffset int64
	var loopErr error
	for p := range q {
		if err := p.compress.Wait(); err != nil {
			loopErr = fmt.Errorf("compress frames: %w", err)
			cancel()

			break
		}
		var compressed [][]byte
		for _, f := range p.frames {
			frameSizes = append(frameSizes, FrameSize{U: int32(f.uncompressedSize), C: int32(len(f.compressed))})
			compressed = append(compressed, f.compressed)
			if sink != nil {
				sink(ctx, cOffset, f.compressed)
			}
			cOffset += int64(len(f.compressed))
		}
		pi := p.index
		work.Go(func() error {
			return uploader.UploadPart(workCtx, pi, compressed...)
		})
	}
	for range q { //nolint:revive // intentional drain
	}
	workErr := work.Wait()
	if err := errors.Join(loopErr, workErr); err != nil {
		return nil, [32]byte{}, err
	}
	if err := uploader.Complete(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("complete upload: %w", err)
	}
	ft := NewFrameTable(cfg.CompressionType(), frameSizes)

	return ft, sum256(hasher), nil
}

func mainReadLoop(ctx context.Context, in io.Reader, cfg CompressConfig, hasher io.Writer, q chan<- *mainPart) error {
	compressors, err := newCompressorPool(cfg)
	if err != nil {
		return err
	}
	frameSize := cfg.FrameSize()
	minPartSize := cfg.MinPartSize()
	workers := max(cfg.FrameEncodeWorkers, 1)
	p, compressCtx := mainNewPart(1, ctx, workers)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		buf := make([]byte, frameSize) // fresh allocation every frame, no pool
		n, err := io.ReadFull(in, buf)
		eof := errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF)
		if err != nil && !eof {
			return fmt.Errorf("read frame: %w", err)
		}
		if n > 0 {
			hasher.Write(buf[:n])
			p.addFrame(compressCtx, buf[:n], compressors)
		}
		if eof {
			if len(p.frames) > 0 {
				select {
				case q <- p:
				case <-ctx.Done():
					return ctx.Err()
				}
			}

			return nil
		}
		if p.compressedSize.Load() >= minPartSize {
			select {
			case q <- p:
			case <-ctx.Done():
				return ctx.Err()
			}
			p, compressCtx = mainNewPart(p.index+1, ctx, workers)
		}
	}
}

// ============================================================================
// Shared harness.
// ============================================================================

// demoBuildInput produces deterministic, mildly-compressible data so zstd
// runs with realistic timing instead of degenerate fast-path on zeros.
func demoBuildInput(bytesTotal int) []byte {
	out := make([]byte, bytesTotal)
	r := rand.New(rand.NewSource(0xCAFEBABE))
	const blockSz = 4096
	for i := 0; i < bytesTotal; i += blockSz {
		end := min(i+blockSz, bytesTotal)
		// repeat a small random block several times to give zstd something to find
		seed := make([]byte, 64)
		r.Read(seed)
		for j := i; j < end; j++ {
			out[j] = seed[(j-i)%len(seed)]
		}
	}

	return out
}

// slowUploader wraps memPartUploader and adds a fixed per-part upload delay
// to simulate GCS multipart upload latency. A 50 MiB part to GCS typically
// takes 300-800 ms in-region; we use 500 ms as a representative figure.
type slowUploader struct {
	inner     memPartUploader
	partDelay time.Duration
}

func (s *slowUploader) Start(ctx context.Context) error { return s.inner.Start(ctx) }
func (s *slowUploader) UploadPart(ctx context.Context, partIndex int, data ...[]byte) error {
	select {
	case <-time.After(s.partDelay):
	case <-ctx.Done():
		return ctx.Err()
	}

	return s.inner.UploadPart(ctx, partIndex, data...)
}
func (s *slowUploader) Complete(ctx context.Context) error { return s.inner.Complete(ctx) }
func (s *slowUploader) Close() error                       { return s.inner.Close() }
func (s *slowUploader) Assemble() []byte                   { return s.inner.Assemble() }

func demoCfg() CompressConfig {
	return CompressConfig{
		Enabled:            true,
		Type:               "zstd",
		Level:              1, // fastest
		FrameSizeKB:        2048,
		MinPartSizeMB:      50,
		FrameEncodeWorkers: 4,
		EncoderConcurrency: 0,
	}
}

type demoStats struct {
	totalAllocBytes uint64
	mallocs         uint64
	heapInUseAfter  uint64
}

func demoMeasure(b func()) demoStats {
	// Explicit GCs isolate this variant's allocation count from previous
	// variants' residue. Two consecutive GCs let the runtime clear any
	// pending finalizer-held memory.
	runtime.GC() //nolint:revive // intentional for measurement isolation
	runtime.GC() //nolint:revive // intentional for measurement isolation
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	b()
	runtime.ReadMemStats(&after)

	return demoStats{
		totalAllocBytes: after.TotalAlloc - before.TotalAlloc,
		mallocs:         after.Mallocs - before.Mallocs,
		heapInUseAfter:  after.HeapInuse,
	}
}

// TestPoolLifecycleDemo runs three designs side-by-side and reports the memory
// cost. Single run, real zstd compression, simulated GCS upload latency.
//
//   - main:        pre-PR-2863, no pool (fresh `make([]byte, frameSize)` per frame).
//   - tomas:       PR #2863, pool + frame.input + releaseInputBuffers checkpoint.
//   - this branch: pool + per-goroutine defer release.
//
// Cannot run in parallel: variants share process-wide runtime.MemStats.
//
//nolint:paralleltest // measurement requires serial execution
func TestPoolLifecycleDemo(t *testing.T) {
	cfg := demoCfg()
	const partUploadDelay = 500 * time.Millisecond // ~realistic GCS multipart part latency

	for _, sz := range []int{256 << 20, 1 << 30} {
		//nolint:paralleltest // measurement requires serial execution
		t.Run(fmt.Sprintf("%dMiB", sz>>20), func(t *testing.T) {
			t.Logf("input=%d MiB, frame=%d KiB, part=%d MiB, workers=%d, upload_delay=%v",
				sz>>20, cfg.FrameSizeKB, cfg.MinPartSizeMB, cfg.FrameEncodeWorkers, partUploadDelay)

			input := demoBuildInput(sz)

			type variantResult struct {
				name  string
				stats demoStats
				ft    *FrameTable
				hash  [32]byte
				dst   []byte
			}

			runVariant := func(name string, fn func(io.Reader, partUploader) (*FrameTable, [32]byte, error)) variantResult {
				u := &slowUploader{partDelay: partUploadDelay}
				var ft *FrameTable
				var hash [32]byte
				st := demoMeasure(func() {
					var err error
					ft, hash, err = fn(bytes.NewReader(input), u)
					if err != nil {
						t.Fatalf("%s: %v", name, err)
					}
				})

				return variantResult{name: name, stats: st, ft: ft, hash: hash, dst: u.Assemble()}
			}

			mainR := runVariant("main         (no pool)         ", func(r io.Reader, u partUploader) (*FrameTable, [32]byte, error) {
				return mainCompressStream(t.Context(), r, cfg, u, 4, nil)
			})
			tomasR := runVariant("tomas (PR #2863, checkpoint)  ", func(r io.Reader, u partUploader) (*FrameTable, [32]byte, error) {
				return oldCompressStream(t.Context(), r, cfg, u, 4, nil)
			})
			branchR := runVariant("this branch (per-goroutine)   ", func(r io.Reader, u partUploader) (*FrameTable, [32]byte, error) {
				return compressStream(t.Context(), r, cfg, u, 4, nil)
			})

			if mainR.hash != tomasR.hash || tomasR.hash != branchR.hash {
				t.Errorf("hash mismatch: main=%x tomas=%x branch=%x", mainR.hash, tomasR.hash, branchR.hash)
			}
			if mainR.ft.NumFrames() != tomasR.ft.NumFrames() || tomasR.ft.NumFrames() != branchR.ft.NumFrames() {
				t.Errorf("frame count mismatch: main=%d tomas=%d branch=%d",
					mainR.ft.NumFrames(), tomasR.ft.NumFrames(), branchR.ft.NumFrames())
			}
			if !bytes.Equal(mainR.dst, tomasR.dst) || !bytes.Equal(tomasR.dst, branchR.dst) {
				t.Errorf("uploaded payload mismatch across variants")
			}

			mib := func(b uint64) float64 { return float64(b) / (1 << 20) }
			for _, v := range []variantResult{mainR, tomasR, branchR} {
				t.Logf("%s: total_alloc=%7.1f MiB  mallocs=%6d  heap_inuse_after=%7.1f MiB",
					v.name, mib(v.stats.totalAllocBytes), v.stats.mallocs, mib(v.stats.heapInUseAfter))
			}
			t.Logf("---")
			t.Logf("tomas vs main:  total_alloc %+7.1f MiB  mallocs %+5d  heap_inuse %+7.1f MiB",
				mib(tomasR.stats.totalAllocBytes)-mib(mainR.stats.totalAllocBytes),
				int64(tomasR.stats.mallocs)-int64(mainR.stats.mallocs),
				mib(tomasR.stats.heapInUseAfter)-mib(mainR.stats.heapInUseAfter))
			t.Logf("branch vs main: total_alloc %+7.1f MiB  mallocs %+5d  heap_inuse %+7.1f MiB",
				mib(branchR.stats.totalAllocBytes)-mib(mainR.stats.totalAllocBytes),
				int64(branchR.stats.mallocs)-int64(mainR.stats.mallocs),
				mib(branchR.stats.heapInUseAfter)-mib(mainR.stats.heapInUseAfter))
			t.Logf("branch vs tomas:total_alloc %+7.1f MiB  mallocs %+5d  heap_inuse %+7.1f MiB",
				mib(branchR.stats.totalAllocBytes)-mib(tomasR.stats.totalAllocBytes),
				int64(branchR.stats.mallocs)-int64(tomasR.stats.mallocs),
				mib(branchR.stats.heapInUseAfter)-mib(tomasR.stats.heapInUseAfter))
		})
	}
}
