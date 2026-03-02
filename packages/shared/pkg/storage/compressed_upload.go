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

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"
	"golang.org/x/sync/errgroup"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

const (
	defaultLZ4CompressionLevel = 3 // lz4 compression level (0=fast, higher=better ratio)
	defaultEncoderConcurrency  = 0 // use default compression concurrency settings
	defaultEncodeWorkers       = 4 // concurrent frame compression workers per file
	defaultUploadPartSize      = 50 * megabyte

	// DefaultCompressFrameSize is the default uncompressed size of each compression
	// frame (2 MiB). Overridable via the frameSizeKB feature flag field.
	// The last frame in a file may be shorter.
	//
	// This MUST be a divisor of MemoryChunkSize and >= every block/page size:
	//   - header.HugepageSize (2 MiB) — UFFD huge-page size
	//   - header.RootfsBlockSize (4 KiB) — NBD / rootfs block size
	DefaultCompressFrameSize = 2 * 1024 * 1024
)

// PartUploader is the interface for uploading data in parts.
// Implementations exist for GCS multipart uploads and local file writes.
type PartUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
}

// FramedUploadOptions configures compression for framed uploads.
// Each frame is FrameSize bytes of uncompressed data (default 2 MiB,
// last frame may be shorter), compressed independently.
type FramedUploadOptions struct {
	CompressionType    CompressionType
	Level              int
	EncoderConcurrency int // goroutines per individual zstd encoder (zstd.WithEncoderConcurrency)
	EncodeWorkers      int // concurrent frame compression workers per CompressStream call
	FrameSize          int // uncompressed frame size in bytes; 0 = DefaultCompressFrameSize
	TargetPartSize     int

	OnFrameReady func(offset FrameOffset, size FrameSize, data []byte) error
}

// DefaultCompressionOptions is the default compression configuration (LZ4).
var DefaultCompressionOptions = &FramedUploadOptions{
	CompressionType:    CompressionLZ4,
	Level:              defaultLZ4CompressionLevel,
	EncoderConcurrency: defaultEncoderConcurrency,
	EncodeWorkers:      defaultEncodeWorkers,
	TargetPartSize:     defaultUploadPartSize,
}

// NoCompression indicates no compression should be applied.
var NoCompression = (*FramedUploadOptions)(nil)

// GetUploadOptions reads the compress-config feature flag and returns
// FramedUploadOptions. Returns nil when compression is disabled.
func GetUploadOptions(ctx context.Context, ff *featureflags.Client) *FramedUploadOptions {
	v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()

	if !v.Get("compressBuilds").BoolValue() {
		return nil
	}

	ct := parseCompressionType(v.Get("compressionType").StringValue())
	if ct == CompressionNone {
		return nil
	}

	return &FramedUploadOptions{
		CompressionType:    ct,
		Level:              v.Get("level").IntValue(),
		FrameSize:          v.Get("frameSizeKB").IntValue() * kilobyte,
		TargetPartSize:     v.Get("uploadPartTargetMB").IntValue() * megabyte,
		EncodeWorkers:      v.Get("encodeWorkers").IntValue(),
		EncoderConcurrency: v.Get("encoderConcurrency").IntValue(),
	}
}

// ValidateCompressionOptions checks that compression options are valid.
func ValidateCompressionOptions(opts *FramedUploadOptions) error {
	if opts == nil || opts.CompressionType == CompressionNone {
		return nil
	}

	if opts.FrameSize <= 0 {
		return fmt.Errorf("frame size must be set, got %d", opts.FrameSize)
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

// compressedFrame is the result of compressing a single frame.
type compressedFrame struct {
	index int
	data  []byte
	sizeU int // uncompressed size of this frame
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

// lz4FrameCompressor uses streaming LZ4 (no EncodeAll equivalent in pierrec/lz4).
type lz4FrameCompressor struct {
	level int
}

func (l *lz4FrameCompressor) Compress(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	buf.Grow(len(src))
	enc := newLZ4Encoder(&buf, l.level)
	if _, err := enc.Write(src); err != nil {
		return nil, fmt.Errorf("lz4 write: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("lz4 close: %w", err)
	}

	return buf.Bytes(), nil
}

// newCompressorPool returns a function that borrows a frameCompressor from a pool
// and a release function to return it. All compressors in the pool share the same
// settings from opts. For zstd, encoders are created once and reused via EncodeAll.
func newCompressorPool(opts *FramedUploadOptions) (borrow func() (frameCompressor, error), release func(frameCompressor)) {
	switch opts.CompressionType {
	case CompressionZstd:
		pool := &sync.Pool{}
		pool.New = func() any {
			enc, err := newZstdEncoder(opts.EncoderConcurrency, opts.FrameSize, zstd.EncoderLevel(opts.Level))
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
		// LZ4 (and any future codecs): lightweight, no pooling needed.
		c := &lz4FrameCompressor{level: opts.Level}

		return func() (frameCompressor, error) { return c, nil },
			func(frameCompressor) {}
	}
}

// CompressStream reads from in, compresses using opts, and writes parts through uploader.
// Returns the resulting FrameTable describing the compressed frames.
//
// The pipeline: reader goroutine → compressor worker pool → collector goroutine → uploader.
// Frames are fixed-size uncompressed (opts.FrameSize, default 2 MiB), compressed concurrently,
// reordered by the collector, and batched into upload PARTs.
func CompressStream(ctx context.Context, in io.Reader, opts *FramedUploadOptions, uploader PartUploader) (*FrameTable, [32]byte, error) {
	targetPartSize := int64(opts.TargetPartSize)
	if targetPartSize == 0 {
		targetPartSize = int64(defaultUploadPartSize)
	}

	workers := opts.EncodeWorkers
	if workers <= 0 {
		workers = defaultEncodeWorkers
	}

	frameSize := opts.FrameSize
	if frameSize <= 0 {
		frameSize = DefaultCompressFrameSize
	}

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to start framed upload: %w", err)
	}

	// Stage 1: Reader goroutine — reads frameSize frames from input.
	type indexedFrame struct {
		index int
		data  []byte
	}
	frameCh := make(chan indexedFrame, workers)
	readErrCh := make(chan error, 1)

	go func() {
		defer close(frameCh)
		for i := 0; ; i++ {
			buf := make([]byte, frameSize)
			n, err := io.ReadFull(in, buf)

			if err == nil {
				if ctxErr := ctx.Err(); ctxErr != nil {
					readErrCh <- ctxErr

					return
				}
				frameCh <- indexedFrame{index: i, data: buf[:n]}

				continue
			}

			if errors.Is(err, io.ErrUnexpectedEOF) {
				if n > 0 {
					frameCh <- indexedFrame{index: i, data: buf[:n]}
				}

				return
			}
			if errors.Is(err, io.EOF) {
				return
			}

			readErrCh <- fmt.Errorf("failed to read frame %d: %w", i, err)

			return
		}
	}()

	// Stage 2: Compressor worker pool — compresses frames concurrently.
	// Compressors are pooled and reused across frames (zstd.EncodeAll is stateless).
	borrow, release := newCompressorPool(opts)

	compressedCh := make(chan compressedFrame, workers)
	compressEG, compressCtx := errgroup.WithContext(ctx)
	compressEG.SetLimit(workers)

	// Launch a goroutine that feeds the worker pool and closes compressedCh when done.
	compressErrCh := make(chan error, 1)
	go func() {
		defer close(compressedCh)

		for f := range frameCh {
			compressEG.Go(func() error {
				if err := compressCtx.Err(); err != nil {
					return err
				}
				c, err := borrow()
				if err != nil {
					return fmt.Errorf("frame %d: %w", f.index, err)
				}
				compressed, err := c.Compress(f.data)
				release(c)
				if err != nil {
					return fmt.Errorf("frame %d: %w", f.index, err)
				}
				compressedCh <- compressedFrame{
					index: f.index,
					data:  compressed,
					sizeU: len(f.data),
				}

				return nil
			})
		}

		if err := compressEG.Wait(); err != nil {
			compressErrCh <- err
		}
	}()

	// Stage 3: Collector — reorders frames, builds FrameTable, batches into PARTs.
	frameTable := &FrameTable{
		CompressionType: opts.CompressionType,
	}

	// Running SHA-256 over compressed data for integrity verification.
	hasher := sha256.New()

	uploadEG, uploadCtx := errgroup.WithContext(ctx)
	uploadEG.SetLimit(4) // max concurrent part uploads

	var (
		reorderBuf = make(map[int]compressedFrame) // out-of-order buffer
		nextIndex  int                             // next frame index to emit
		offset     FrameOffset                     // cumulative offset for OnFrameReady
		readyParts [][]byte                        // accumulated frames for current PART
		partLen    int64
		partIndex  int
	)

	emitFrame := func(cf compressedFrame) error {
		fs := FrameSize{
			U: int32(cf.sizeU),
			C: int32(len(cf.data)),
		}
		frameTable.Frames = append(frameTable.Frames, fs)

		// Feed compressed bytes to running checksum (piggybacking on existing iteration).
		hasher.Write(cf.data)

		if opts.OnFrameReady != nil {
			if err := opts.OnFrameReady(offset, fs, cf.data); err != nil {
				return fmt.Errorf("OnFrameReady callback failed: %w", err)
			}
		}

		offset.Add(fs)
		partLen += int64(len(cf.data))
		readyParts = append(readyParts, cf.data)

		return nil
	}

	flushPart := func(last bool) {
		if len(readyParts) == 0 {
			return
		}
		if partLen < targetPartSize && !last {
			return
		}

		partIndex++
		i := partIndex
		frameData := append([][]byte{}, readyParts...)
		partLen = 0
		readyParts = readyParts[:0]

		uploadEG.Go(func() error {
			if err := uploader.UploadPart(uploadCtx, i, frameData...); err != nil {
				return fmt.Errorf("failed to upload part %d: %w", i, err)
			}

			return nil
		})
	}

	// Drain compressed frames, reorder, and emit.
	var collectErr error
	for cf := range compressedCh {
		reorderBuf[cf.index] = cf

		// Emit as many sequential frames as possible.
		for {
			next, ok := reorderBuf[nextIndex]
			if !ok {
				break
			}
			delete(reorderBuf, nextIndex)
			nextIndex++

			if err := emitFrame(next); err != nil {
				collectErr = err

				break
			}
			flushPart(false)
		}
		if collectErr != nil {
			break
		}
	}

	// Check for errors from earlier stages.
	select {
	case err := <-readErrCh:
		return nil, [32]byte{}, err
	default:
	}
	select {
	case err := <-compressErrCh:
		return nil, [32]byte{}, err
	default:
	}
	if collectErr != nil {
		return nil, [32]byte{}, collectErr
	}

	// Flush the last part.
	flushPart(true)

	if err := uploadEG.Wait(); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to upload frames: %w", err)
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
		var buf bytes.Buffer
		buf.Grow(len(data))
		w := newLZ4Encoder(&buf, level)
		if _, err := w.Write(data); err != nil {
			return nil, fmt.Errorf("lz4 compress: %w", err)
		}
		if err := w.Close(); err != nil {
			return nil, fmt.Errorf("lz4 close: %w", err)
		}

		return buf.Bytes(), nil

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

func newLZ4Encoder(out io.Writer, level int) io.WriteCloser {
	w := lz4.NewWriter(out)
	opts := []lz4.Option{lz4.ConcurrencyOption(1)}
	if level > 0 {
		opts = append(opts, lz4.CompressionLevelOption(lz4.CompressionLevel(1<<(8+level))))
	}
	_ = w.Apply(opts...)

	return w
}
