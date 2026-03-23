package storage

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"
	"golang.org/x/sync/errgroup"
)

// MaxCompressedHeaderSize is the maximum allowed decompressed header size (64 MiB).
// Headers are typically a few hundred KiB; this is a safety bound.
const MaxCompressedHeaderSize = 64 << 20

// CompressLZ4 compresses data using LZ4 block compression.
// Returns an error if the data is incompressible (CompressBlock returns 0),
// since callers store the result as ".lz4" and DecompressLZ4 would fail on raw data.
func CompressLZ4(data []byte) ([]byte, error) {
	bound := lz4.CompressBlockBound(len(data))
	dst := make([]byte, bound)

	n, err := lz4.CompressBlock(data, dst, nil)
	if err != nil {
		return nil, fmt.Errorf("lz4 compress: %w", err)
	}

	if n == 0 {
		return nil, fmt.Errorf("lz4 compress: data is incompressible (%d bytes)", len(data))
	}

	return dst[:n], nil
}

const (
	defaultFrameEncodeWorkers = 4        // concurrent frame-level compression workers per CompressStream call
	defaultTargetPartSize     = 50 << 20 // 50 MiB compressed target per upload part

	// DefaultCompressFrameSize is the default uncompressed size of each compression
	// frame (2 MiB). Overridable via CompressConfig.FrameSizeKB.
	// The last frame in a file may be shorter.
	//
	// The chunker fetches one frame at a time from storage on a cache miss.
	// Larger frame sizes mean more data cached per fetch (faster warm-up and
	// fewer GCS round-trips), but higher memory and I/O cost per miss.
	//
	// This MUST be a divisor of MemoryChunkSize and >= every block/page size:
	//   - header.HugepageSize (2 MiB) — UFFD huge-page size
	//   - header.RootfsBlockSize (4 KiB) — NBD / rootfs block size
	DefaultCompressFrameSize = 2 * 1024 * 1024

	// File type identifiers for per-file-type compression targeting.
	FileTypeMemfile = "memfile"
	FileTypeRootfs  = "rootfs"

	// Use case identifiers for per-use-case compression targeting.
	UseCaseBuild = "build"
	UseCasePause = "pause"
)

// PartUploader is the interface for uploading data in parts.
// Implementations exist for GCS multipart uploads and local file writes.
type PartUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
	Close() error
}

// OnFrameCompressed is an optional progress callback invoked for each compressed frame
// during CompressStream. Not part of the StoreFile interface — only available when
// calling CompressStream directly.
type OnFrameCompressed = func(frameIndex int, offset FrameOffset, size FrameSize)

// ValidateCompressConfig checks that compression config is valid for use.
func ValidateCompressConfig(cfg *CompressConfig) error {
	if cfg == nil || !cfg.IsEnabled() {
		return nil
	}

	if cfg.FrameSize() <= 0 {
		return fmt.Errorf("frame size must be set, got %d KB", cfg.FrameSizeKB)
	}

	return nil
}

// MemPartUploader collects compressed parts in memory. Thread-safe.
// Useful for tests and benchmarks that need CompressStream output as bytes.
type MemPartUploader struct {
	mu    sync.Mutex
	parts map[int][]byte
}

func (m *MemPartUploader) Start(context.Context) error {
	m.parts = make(map[int][]byte)

	return nil
}

func (m *MemPartUploader) UploadPart(_ context.Context, partIndex int, data ...[]byte) error {
	var buf bytes.Buffer
	for _, d := range data {
		buf.Write(d)
	}
	m.mu.Lock()
	m.parts[partIndex] = buf.Bytes()
	m.mu.Unlock()

	return nil
}

func (m *MemPartUploader) Complete(context.Context) error { return nil }
func (m *MemPartUploader) Close() error                   { return nil }

// Assemble returns the concatenated parts in index order.
func (m *MemPartUploader) Assemble() []byte {
	keys := make([]int, 0, len(m.parts))
	for k := range m.parts {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	var buf bytes.Buffer
	for _, k := range keys {
		buf.Write(m.parts[k])
	}

	return buf.Bytes()
}

// frameCompressor compresses individual frames. Implementations are pooled
// and reused across frames within a single CompressStream call.
type frameCompressor interface {
	// Compress compresses src and returns the compressed bytes.
	Compress(src []byte) ([]byte, error)
}

// zstdFrameCompressor wraps a pooled zstd.Encoder using EncodeAll.
type zstdFrameCompressor struct {
	enc  *zstd.Encoder
	pool *sync.Pool
}

func (z *zstdFrameCompressor) Compress(src []byte) ([]byte, error) {
	// EncodeAll is stateless on the encoder — safe to reuse without reset.
	return z.enc.EncodeAll(src, make([]byte, 0, len(src))), nil
}

func (z *zstdFrameCompressor) release() {
	z.pool.Put(z)
}

// lz4FrameCompressor uses raw LZ4 block compression (no frame headers/checksums).
type lz4FrameCompressor struct {
	pool *sync.Pool
}

func (l *lz4FrameCompressor) Compress(src []byte) ([]byte, error) {
	// CompressBlockBound guarantees enough space — n == 0 cannot happen.
	dst := make([]byte, lz4.CompressBlockBound(len(src)))

	n, err := lz4.CompressBlock(src, dst, nil)
	if err != nil {
		return nil, fmt.Errorf("lz4 block compress: %w", err)
	}

	return dst[:n], nil
}

func (l *lz4FrameCompressor) release() {
	l.pool.Put(l)
}

// newCompressorPool returns a function that borrows a frameCompressor from a pool
// and a release function to return it. All compressors in the pool share the same
// settings from cfg. For zstd, encoders are created once and reused via EncodeAll.
func newCompressorPool(cfg *CompressConfig) (borrow func() (frameCompressor, error), release func(frameCompressor)) {
	switch cfg.CompressionType() {
	case CompressionZstd:
		pool := &sync.Pool{}
		pool.New = func() any {
			enc, err := newZstdEncoder(cfg.EncoderConcurrency, cfg.FrameSize(), zstd.EncoderLevel(cfg.Level))
			if err != nil {
				// Pool.New cannot return errors; store nil and check on borrow.
				return err
			}

			return &zstdFrameCompressor{enc: enc, pool: pool}
		}

		return func() (frameCompressor, error) {
				v := pool.Get()
				if err, ok := v.(error); ok {
					return nil, fmt.Errorf("zstd encoder pool: %w", err)
				}

				return v.(*zstdFrameCompressor), nil
			}, func(c frameCompressor) {
				if z, ok := c.(*zstdFrameCompressor); ok {
					z.release()
				}
			}
	default:
		// LZ4: CompressBlock uses internal hash tables, not goroutine-safe — pool them.
		pool := &sync.Pool{}
		pool.New = func() any {
			return &lz4FrameCompressor{pool: pool}
		}

		return func() (frameCompressor, error) {
				return pool.Get().(*lz4FrameCompressor), nil
			}, func(c frameCompressor) {
				if l, ok := c.(*lz4FrameCompressor); ok {
					l.release()
				}
			}
	}
}

// CompressStream reads from in, compresses using cfg, and writes parts through uploader.
// Returns the resulting FrameTable describing the compressed frames.
//
// Design: frame-at-a-time with target part size accumulation.
//
// The main goroutine reads frames one at a time from in, submits each to a
// concurrency-limited compress worker pool (errgroup with SetLimit). When a
// worker finishes it atomically adds its compressed size to a running counter.
// errgroup.Go() blocks when all workers are busy, so the main goroutine
// naturally checks the counter after each completion.
//
// When the accumulated compressed size reaches targetPartSize, the current
// part is "closed": a background goroutine waits for the part's remaining
// in-flight workers, then emits frames and uploads. The main goroutine
// immediately starts a new part and continues reading, borrowing compressors
// from the shared pool as they become available.
//
// Part emission is chained: part K+1 waits for part K's emission to complete,
// ensuring frameTable and offset are updated in order.
func CompressStream(ctx context.Context, in io.Reader, cfg *CompressConfig, onFrame OnFrameCompressed, uploader PartUploader) (*FrameTable, [32]byte, error) {
	workers := cfg.FrameEncodeWorkers
	if workers <= 0 {
		workers = defaultFrameEncodeWorkers
	}

	frameSize := cfg.FrameSize()

	targetPartSize := int64(cfg.TargetPartSizeMB) * (1 << 20)
	if targetPartSize <= 0 {
		targetPartSize = int64(defaultTargetPartSize)
	}

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to start framed upload: %w", err)
	}
	defer uploader.Close()

	borrow, release := newCompressorPool(cfg)
	hasher := sha256.New()

	frameTable := &FrameTable{compressionType: cfg.CompressionType()}
	uploadEG, uploadCtx := errgroup.WithContext(ctx)
	uploadEG.SetLimit(4) // max concurrent part uploads

	// pendingFrame tracks one frame submitted to the compress workers.
	// The main goroutine allocates and appends; the worker writes compressed via the captured pointer.
	type pendingFrame struct {
		uncompressedSize int
		compressed       []byte
	}

	var (
		offset     FrameOffset
		partIndex  int
		frameIndex int
	)

	// Per-part state. Reset when a part is flushed.
	var partFrames []*pendingFrame
	var partCompressedSize atomic.Int64
	compressEG, compressCtx := errgroup.WithContext(ctx)
	compressEG.SetLimit(workers)

	// Emission chain: each part's background goroutine waits for the previous
	// part to finish emitting before it emits, ensuring frameTable/offset order.
	var prevEmitDone chan struct{}

	// flushPart closes the current part: launches a background goroutine that
	// waits for compression, emits frames in order, and uploads.
	// The main goroutine can immediately continue reading for the next part.
	flushPart := func() {
		frames := partFrames
		eg := compressEG
		prev := prevEmitDone
		emitDone := make(chan struct{})
		prevEmitDone = emitDone

		partIndex++
		pi := partIndex

		uploadEG.Go(func() error {
			// Wait for all compression workers for this part.
			if err := eg.Wait(); err != nil {
				close(emitDone)

				return err
			}

			// Wait for previous part's emission to complete (ordering).
			if prev != nil {
				select {
				case <-prev:
				case <-uploadCtx.Done():
					close(emitDone)

					return uploadCtx.Err()
				}
			}

			// Emit frames in order — safe: only one goroutine emits at a time.
			partData := make([][]byte, len(frames))
			var partBytes int
			for i, f := range frames {
				fs := FrameSize{U: int32(f.uncompressedSize), C: int32(len(f.compressed))}
				frameTable.Frames = append(frameTable.Frames, fs)

				if onFrame != nil {
					onFrame(frameIndex, offset, fs)
				}

				frameIndex++
				offset.Add(fs)
				partData[i] = f.compressed
				partBytes += len(f.compressed)
			}

			close(emitDone)

			return uploader.UploadPart(uploadCtx, pi, partData...)
		})

		// Reset per-part state for the next part.
		partFrames = nil
		partCompressedSize.Store(0)
		compressEG, compressCtx = errgroup.WithContext(ctx)
		compressEG.SetLimit(workers)
	}

	// --- Main read loop: one frame at a time ---
	for {
		if err := ctx.Err(); err != nil {
			return nil, [32]byte{}, err
		}

		buf := make([]byte, frameSize)
		n, readErr := io.ReadFull(in, buf)

		if n > 0 {
			hasher.Write(buf[:n])
			frameData := buf[:n]

			pf := &pendingFrame{uncompressedSize: n}
			partFrames = append(partFrames, pf)

			cCtx := compressCtx // capture for closure
			compressEG.Go(func() error {
				if err := cCtx.Err(); err != nil {
					return err
				}
				c, err := borrow()
				if err != nil {
					return err
				}
				out, err := c.Compress(frameData)
				release(c)
				if err != nil {
					return err
				}
				pf.compressed = out
				partCompressedSize.Add(int64(len(out)))

				return nil
			})

			// Check if we've accumulated enough for this part.
			// errgroup.Go blocks when workers are full, so by the time
			// we get here a worker may have finished and updated the counter.
			eof := readErr != nil
			if !eof && partCompressedSize.Load() >= targetPartSize {
				flushPart()
			}
		}

		if readErr != nil {
			if !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
				return nil, [32]byte{}, fmt.Errorf("read frame: %w", readErr)
			}

			break
		}
	}

	// Flush final part (no minimum size constraint).
	if len(partFrames) > 0 {
		flushPart()
	}

	if err := uploadEG.Wait(); err != nil {
		return nil, [32]byte{}, fmt.Errorf("upload: %w", err)
	}

	if err := uploader.Complete(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to finish uploading frames: %w", err)
	}

	var checksum [32]byte
	copy(checksum[:], hasher.Sum(nil))

	return frameTable, checksum, nil
}

// newZstdEncoder creates a zstd encoder for use with EncodeAll.
// The encoder is created with a nil writer since EncodeAll doesn't use streaming output.
func newZstdEncoder(concurrency int, windowSize int, compressionLevel zstd.EncoderLevel) (*zstd.Encoder, error) {
	zstdOpts := []zstd.EOption{
		zstd.WithEncoderLevel(compressionLevel),
	}
	if windowSize > 0 {
		zstdOpts = append(zstdOpts, zstd.WithWindowSize(windowSize))
	}
	if concurrency > 0 {
		zstdOpts = append(zstdOpts, zstd.WithEncoderConcurrency(concurrency))
	}

	return zstd.NewWriter(nil, zstdOpts...)
}

// CompressRawNoFrames compresses data as a single stream (no framing) using the given
// codec and level. Uses the same encoder settings as CompressStream (window
// size, concurrency) so raw vs framed comparisons are fair. It is used only in
// benchmarks.
func CompressRawNoFrames(ct CompressionType, level int, data []byte) ([]byte, error) {
	switch ct {
	case CompressionLZ4:
		dst := make([]byte, lz4.CompressBlockBound(len(data)))
		n, err := lz4.CompressBlock(data, dst, nil)
		if err != nil {
			return nil, fmt.Errorf("lz4 block compress: %w", err)
		}

		return dst[:n], nil

	case CompressionZstd:
		enc, err := newZstdEncoder(0, DefaultCompressFrameSize, zstd.EncoderLevel(level))
		if err != nil {
			return nil, fmt.Errorf("zstd encoder: %w", err)
		}
		defer enc.Close()

		return enc.EncodeAll(data, make([]byte, 0, len(data))), nil

	default:
		return nil, fmt.Errorf("unsupported compression type: %s", ct)
	}
}
