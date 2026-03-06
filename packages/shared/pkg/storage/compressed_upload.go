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
	defaultLZ4CompressionLevel = 0  // lz4 compression level (0=fast/default, higher=better ratio)
	defaultEncoderConcurrency  = 0  // use default compression concurrency settings
	defaultFrameEncodeWorkers  = 4  // concurrent frame-level compression workers per CompressStream call
	defaultFramesPerUploadPart = 25 // frames per upload part (25 × 2 MiB = 50 MiB uncompressed per part)

	// DefaultCompressFrameSize is the default uncompressed size of each compression
	// frame (2 MiB). Overridable via the frameSizeKB feature flag field.
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

// FramedUploadOptions configures compression for framed uploads.
// Each frame is FrameSize bytes of uncompressed data (default 2 MiB,
// last frame may be shorter), compressed independently.
type FramedUploadOptions struct {
	CompressionType     CompressionType
	CompressionLevel    int // codec-specific level (zstd: 1=fastest..4=best; lz4: 0=default, higher=better ratio)
	EncoderConcurrency  int // goroutines per individual zstd/lz4 encoder
	FrameEncodeWorkers  int // concurrent frame-level compression workers (parallel frames per CompressStream call)
	FrameSize           int // uncompressed frame size in bytes; 0 = DefaultCompressFrameSize
	FramesPerUploadPart int // frames per upload part; 0 = defaultFramesPerUploadPart (25)

	OnFrameReady func(offset FrameOffset, size FrameSize, data []byte) error
}

// DefaultCompressionOptions is the default compression configuration (LZ4).
var DefaultCompressionOptions = &FramedUploadOptions{
	CompressionType:     CompressionLZ4,
	CompressionLevel:    defaultLZ4CompressionLevel,
	EncoderConcurrency:  defaultEncoderConcurrency,
	FrameEncodeWorkers:  defaultFrameEncodeWorkers,
	FramesPerUploadPart: defaultFramesPerUploadPart,
}

// NoCompression indicates no compression should be applied.
var NoCompression = (*FramedUploadOptions)(nil)

// GetUploadOptions reads the compress-config feature flag and returns
// FramedUploadOptions. Returns nil when compression is disabled or ff is nil.
//
// fileType and useCase are added to the LD evaluation context so that
// LaunchDarkly targeting rules can differentiate (e.g. compress memfile
// but not rootfs, or compress builds but not pauses). Zero override
// logic in Go — all differentiation is handled by LD dashboard rules.
//
// TODO: compression settings should be part of the core orchestrator
// deployment config (configurable via deployment options like everything
// else). FFs remain as the override/experimentation layer on top.
func GetUploadOptions(ctx context.Context, ff *featureflags.Client, fileType, useCase string) *FramedUploadOptions {
	if ff == nil {
		return nil
	}

	ctx = featureflags.AddToContext(ctx,
		featureflags.CompressFileTypeContext(fileType),
		featureflags.CompressUseCaseContext(useCase),
	)

	v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()

	if !v.Get("compressBuilds").BoolValue() {
		return nil
	}

	ct := parseCompressionType(v.Get("compressionType").StringValue())
	if ct == CompressionNone {
		return nil
	}

	return &FramedUploadOptions{
		CompressionType:     ct,
		CompressionLevel:    v.Get("compressionLevel").IntValue(),
		FrameSize:           v.Get("frameSizeKB").IntValue() * kilobyte,
		FramesPerUploadPart: v.Get("framesPerUploadPart").IntValue(),
		FrameEncodeWorkers:  v.Get("frameEncodeWorkers").IntValue(),
		EncoderConcurrency:  v.Get("encoderConcurrency").IntValue(),
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
// settings from opts. For zstd, encoders are created once and reused via EncodeAll.
func newCompressorPool(opts *FramedUploadOptions) (borrow func() (frameCompressor, error), release func(frameCompressor)) {
	switch opts.CompressionType {
	case CompressionZstd:
		pool := &sync.Pool{}
		pool.New = func() any {
			enc, err := newZstdEncoder(opts.EncoderConcurrency, opts.FrameSize, zstd.EncoderLevel(opts.CompressionLevel))
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

// CompressStream reads from in, compresses using opts, and writes parts through uploader.
// Returns the resulting FrameTable describing the compressed frames.
//
// Design: single-loop, batch-parallel. Each iteration reads a batch of frames
// (one batch = one upload part), compresses them in parallel, emits in order,
// and uploads asynchronously. Upload of part K overlaps with read+compress of
// batch K+1. No channels, no reorder buffer.
func CompressStream(ctx context.Context, in io.Reader, opts *FramedUploadOptions, uploader PartUploader) (*FrameTable, [32]byte, error) {
	workers := opts.FrameEncodeWorkers
	if workers <= 0 {
		workers = defaultFrameEncodeWorkers
	}

	frameSize := opts.FrameSize
	if frameSize <= 0 {
		frameSize = DefaultCompressFrameSize
	}

	if err := uploader.Start(ctx); err != nil {
		return nil, [32]byte{}, fmt.Errorf("failed to start framed upload: %w", err)
	}
	defer uploader.Close()

	borrow, release := newCompressorPool(opts)
	hasher := sha256.New()

	frameTable := &FrameTable{CompressionType: opts.CompressionType}
	uploadEG, uploadCtx := errgroup.WithContext(ctx)
	uploadEG.SetLimit(4) // max concurrent part uploads

	var (
		offset    FrameOffset
		partIndex int
	)

	framesPerPart := opts.FramesPerUploadPart
	if framesPerPart <= 0 {
		framesPerPart = defaultFramesPerUploadPart
	}

	for {
		// --- Read frames and submit to compress workers immediately ---
		// While the main goroutine reads frame K, workers compress frames 0..K-1.
		batchLen := 0
		sizes := make([]int, framesPerPart)
		compressed := make([][]byte, framesPerPart)
		compressEG, compressCtx := errgroup.WithContext(ctx)
		compressEG.SetLimit(workers)
		eof := false

		for i := range framesPerPart {
			if err := ctx.Err(); err != nil {
				return nil, [32]byte{}, err
			}

			buf := make([]byte, frameSize)
			n, err := io.ReadFull(in, buf)

			if n > 0 {
				hasher.Write(buf[:n])
				sizes[i] = n
				batchLen++

				frameData := buf[:n]
				idx := i
				compressEG.Go(func() error {
					if err := compressCtx.Err(); err != nil {
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
					compressed[idx] = out

					return nil
				})
			}

			if err != nil {
				if !errors.Is(err, io.ErrUnexpectedEOF) && !errors.Is(err, io.EOF) {
					return nil, [32]byte{}, fmt.Errorf("read frame: %w", err)
				}
				eof = true

				break
			}
		}

		if batchLen == 0 {
			break
		}

		if err := compressEG.Wait(); err != nil {
			return nil, [32]byte{}, err
		}

		// --- Emit in order, call OnFrameReady ---
		partData := make([][]byte, batchLen)
		for i := range batchLen {
			fs := FrameSize{U: int32(sizes[i]), C: int32(len(compressed[i]))}
			frameTable.Frames = append(frameTable.Frames, fs)

			if opts.OnFrameReady != nil {
				if err := opts.OnFrameReady(offset, fs, compressed[i]); err != nil {
					return nil, [32]byte{}, err
				}
			}

			offset.Add(fs)
			partData[i] = compressed[i]
		}

		// --- Upload part asynchronously ---
		partIndex++
		pi := partIndex
		uploadEG.Go(func() error {
			return uploader.UploadPart(uploadCtx, pi, partData...)
		})

		if eof {
			break
		}
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
