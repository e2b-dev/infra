package storage

import (
	"bytes"
	"context"
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
	defaultTargetFrameSizeC       = 2 * megabyte // target compressed frame size
	defaultLZ4CompressionLevel    = 3            // lz4 compression level (0=fast, higher=better ratio)
	defaultCompressionConcurrency = 0            // use default compression concurrency settings
	defaultUploadPartSize         = 50 * megabyte

	// DefaultMaxFrameUncompressedSize caps the uncompressed bytes in a single frame.
	// When a frame's uncompressed size reaches this limit it is flushed regardless
	// of the compressed size.  4× MemoryChunkSize = 16 MiB.
	DefaultMaxFrameUncompressedSize = 4 * MemoryChunkSize

	// FrameAlignmentSize is the read granularity for compression input.
	// Frames are composed of whole chunks of this size, guaranteeing that
	// no request served by the chunker (UFFD, NBD, prefetch) ever crosses
	// a frame boundary.
	//
	// This MUST be >= every block/page size the system uses:
	//   - MemoryChunkSize  (4 MiB)  — uncompressed fetch unit
	//   - header.HugepageSize (2 MiB) — UFFD huge-page size
	//   - header.RootfsBlockSize (4 KiB) — NBD / rootfs block size
	//
	// Do NOT increase this without also ensuring all compressed frame
	// sizes remain exact multiples.  Changing it is not free.
	FrameAlignmentSize = 1 * MemoryChunkSize
)

// PartUploader is the interface for uploading data in parts.
// Implementations exist for GCS multipart uploads and local file writes.
type PartUploader interface {
	Start(ctx context.Context) error
	UploadPart(ctx context.Context, partIndex int, data ...[]byte) error
	Complete(ctx context.Context) error
}

// FramedUploadOptions configures compression for framed uploads.
// Input is read in FrameAlignmentSize chunks; frames are always composed
// of whole chunks so no chunker request ever crosses a frame boundary.
type FramedUploadOptions struct {
	CompressionType        CompressionType
	Level                  int
	CompressionConcurrency int
	TargetFrameSize        int // frames may be bigger than this due to chunk alignment and async compression.
	TargetPartSize         int

	// MaxUncompressedFrameSize caps uncompressed bytes per frame.
	// 0 = use DefaultMaxFrameUncompressedSize.
	MaxUncompressedFrameSize int

	OnFrameReady func(offset FrameOffset, size FrameSize, data []byte) error
}

// DefaultCompressionOptions is the default compression configuration (LZ4).
var DefaultCompressionOptions = &FramedUploadOptions{
	CompressionType:          CompressionLZ4,
	TargetFrameSize:          defaultTargetFrameSizeC,
	Level:                    defaultLZ4CompressionLevel,
	CompressionConcurrency:   defaultCompressionConcurrency,
	TargetPartSize:           defaultUploadPartSize,
	MaxUncompressedFrameSize: DefaultMaxFrameUncompressedSize,
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

	intOr := func(key string, fallback int) int {
		if n := v.Get(key).IntValue(); n != 0 {
			return n
		}

		return fallback
	}
	strOr := func(key, fallback string) string {
		if s := v.Get(key).StringValue(); s != "" {
			return s
		}

		return fallback
	}

	ct := parseCompressionType(strOr("compressionType", "lz4"))
	if ct == CompressionNone {
		return nil
	}

	return &FramedUploadOptions{
		CompressionType:          ct,
		Level:                    intOr("level", 3),
		TargetFrameSize:          intOr("frameTargetMB", 2) * megabyte,
		TargetPartSize:           intOr("uploadPartTargetMB", 50) * megabyte,
		MaxUncompressedFrameSize: intOr("frameMaxUncompressedMB", 16) * megabyte,
		CompressionConcurrency:   intOr("encoderConcurrency", 1),
	}
}

// InitDecoders reads the compress-config feature flag and sets the pooled
// zstd decoder concurrency. Call once at startup before any reads.
func InitDecoders(ctx context.Context, ff *featureflags.Client) {
	v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()
	n := max(v.Get("decoderConcurrency").IntValue(), 1)
	SetDecoderConcurrency(n)
}

// ValidateCompressionOptions checks that compression options are valid.
func ValidateCompressionOptions(opts *FramedUploadOptions) error {
	if opts == nil || opts.CompressionType == CompressionNone {
		return nil
	}

	return nil
}

// CompressBytes compresses data using opts and returns the concatenated
// compressed bytes along with the FrameTable. This is a convenience wrapper
// around CompressStream that collects all parts in memory.
func CompressBytes(ctx context.Context, data []byte, opts *FramedUploadOptions) ([]byte, *FrameTable, error) {
	up := &memPartUploader{}

	ft, err := CompressStream(ctx, bytes.NewReader(data), opts, up)
	if err != nil {
		return nil, nil, err
	}

	return up.assemble(), ft, nil
}

// memPartUploader collects compressed parts in memory.
type memPartUploader struct {
	parts map[int][]byte
}

func (m *memPartUploader) Start(context.Context) error {
	m.parts = make(map[int][]byte)
	return nil
}

func (m *memPartUploader) UploadPart(_ context.Context, partIndex int, data ...[]byte) error {
	var buf bytes.Buffer
	for _, d := range data {
		buf.Write(d)
	}
	m.parts[partIndex] = buf.Bytes()
	return nil
}

func (m *memPartUploader) Complete(context.Context) error { return nil }

func (m *memPartUploader) assemble() []byte {
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

// CompressStream reads from in, compresses using opts, and writes parts through uploader.
// Returns the resulting FrameTable describing the compressed frames.
func CompressStream(ctx context.Context, in io.Reader, opts *FramedUploadOptions, uploader PartUploader) (*FrameTable, error) {
	targetPartSize := int64(opts.TargetPartSize)
	if targetPartSize == 0 {
		targetPartSize = int64(defaultUploadPartSize)
	}
	enc := newFrameEncoder(opts, uploader, targetPartSize, 4)

	return enc.uploadFramed(ctx, in)
}

type encoder struct {
	opts                 *FramedUploadOptions
	maxUploadConcurrency int

	// frame rotation is protected by mutex
	mu          sync.Mutex
	frame       *frame
	frameTable  *FrameTable
	readyFrames [][]byte
	offset      FrameOffset // tracks cumulative offset for OnFrameReady callback

	// Upload-specific data
	targetPartSize int64
	partIndex      int
	partLen        int64
	uploader       PartUploader
}

type frame struct {
	e                *encoder
	enc              io.WriteCloser
	compressedBuffer *bytes.Buffer
	flushing         bool

	// lenU is updated by the Copy goroutine when it writes uncompressed data
	// into the _current_ frame; can be read without locking after the frame
	// starts closing since the incoming data is going to a new frame.
	lenU int

	// lenC is updated in the Write() method as compressed data is written into
	// the compressedBuffer. It can be read without locking after the frame's
	// encoder is flushed (closed).
	lenC int
}

var _ io.Writer = (*frame)(nil) // for compression output

func newFrameEncoder(opts *FramedUploadOptions, u PartUploader, targetPartSize int64, maxUploadConcurrency int) *encoder {
	return &encoder{
		opts:                 opts,
		maxUploadConcurrency: maxUploadConcurrency,
		targetPartSize:       targetPartSize,
		readyFrames:          make([][]byte, 0, 8),
		uploader:             u,
		frameTable: &FrameTable{
			CompressionType: opts.CompressionType,
		},
	}
}

func (e *encoder) uploadFramed(ctx context.Context, in io.Reader) (*FrameTable, error) {
	// Set up the uploader
	uploadEG, uploadCtx := errgroup.WithContext(ctx)
	if e.maxUploadConcurrency > 0 {
		uploadEG.SetLimit(e.maxUploadConcurrency)
	}

	err := e.uploader.Start(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start framed upload: %w", err)
	}

	// Start copying file to the compression encoder. Use a return channel
	// instead of errgroup to be able to detect completion in the event loop.
	// Buffer 8 chunks to allow read-ahead and better pipelining.
	chunkCh := make(chan []byte, 8)
	readErrorCh := make(chan error, 1)
	go e.readFile(ctx, in, FrameAlignmentSize, chunkCh, readErrorCh)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()

		case err = <-readErrorCh:
			return nil, err

		case chunk, haveData := <-chunkCh:
			// See if we need to flush and to start a new frame
			e.mu.Lock()
			var flush *frame
			if haveData {
				if e.frame == nil || e.frame.flushing {
					// Start a new frame and flush the current one
					flush = e.frame
					if e.frame, err = e.startFrame(); err != nil {
						e.mu.Unlock()

						return nil, fmt.Errorf("failed to start frame: %w", err)
					}
				}
			} else {
				// No more data; flush current frame
				flush = e.frame
			}
			frame := e.frame
			e.mu.Unlock()

			if flush != nil {
				if err = e.flushFrame(uploadEG, uploadCtx, flush, !haveData); err != nil {
					return nil, fmt.Errorf("failed to flush frame: %w", err)
				}
			}

			// If we have data, write it to the current frame and continue
			if haveData {
				if err = e.writeChunk(frame, chunk); err != nil {
					return nil, fmt.Errorf("failed to encode to frame: %w", err)
				}

				continue
			}

			// No more data to process; wait for the uploads to complete and done!
			if err = uploadEG.Wait(); err != nil {
				return nil, fmt.Errorf("failed to upload frames: %w", err)
			}

			if e.uploader != nil {
				if err = e.uploader.Complete(ctx); err != nil {
					return nil, fmt.Errorf("failed to finish uploading frames: %w", err)
				}
			}

			return e.frameTable, nil
		}
	}
}

func (e *encoder) flushFrame(eg *errgroup.Group, uploadCtx context.Context, f *frame, last bool) error {
	if err := f.enc.Close(); err != nil {
		return fmt.Errorf("failed to close encoder: %w", err)
	}

	ft := FrameSize{
		U: int32(f.lenU),
		C: int32(f.lenC),
	}

	e.frameTable.Frames = append(e.frameTable.Frames, ft)

	data := f.compressedBuffer.Bytes()

	// Notify callback if provided (e.g., for cache write-through)
	if e.opts.OnFrameReady != nil {
		if err := e.opts.OnFrameReady(e.offset, ft, data); err != nil {
			return fmt.Errorf("OnFrameReady callback failed: %w", err)
		}
	}

	// Advance offset for next frame
	e.offset.Add(ft)

	e.partLen += int64(len(data))
	e.readyFrames = append(e.readyFrames, data)

	if e.partLen >= e.targetPartSize || last {
		e.partIndex++

		i := e.partIndex
		frameData := append([][]byte{}, e.readyFrames...)
		e.partLen = 0
		e.readyFrames = e.readyFrames[:0]

		eg.Go(func() error {
			err := e.uploader.UploadPart(uploadCtx, i, frameData...)
			if err != nil {
				return fmt.Errorf("failed to upload part %d: %w", i, err)
			}

			return nil
		})
	}

	return nil
}

func (e *encoder) readFile(ctx context.Context, in io.Reader, chunkSize int, chunkCh chan<- []byte, errorCh chan<- error) {
	for i := 0; ; i++ {
		chunk := make([]byte, chunkSize)
		n, err := io.ReadFull(in, chunk)

		if err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				errorCh <- ctxErr

				return
			}
			chunkCh <- chunk[:n]

			continue
		}

		// ErrUnexpectedEOF means a partial read (last chunk shorter than chunkSize).
		if errors.Is(err, io.ErrUnexpectedEOF) {
			if n > 0 {
				chunkCh <- chunk[:n]
			}
			close(chunkCh)

			return
		}
		// EOF means no bytes were read at all.
		if errors.Is(err, io.EOF) {
			close(chunkCh)

			return
		}

		errorCh <- fmt.Errorf("failed to read file chunk %d: %w", i, err)

		return
	}
}

func (e *encoder) startFrame() (*frame, error) {
	var enc io.WriteCloser
	var err error
	frame := &frame{
		e:                e,
		compressedBuffer: bytes.NewBuffer(make([]byte, 0, e.opts.TargetFrameSize+e.opts.TargetFrameSize/2)), // pre-allocate buffer to avoid resizes during compression
	}
	switch e.opts.CompressionType {
	case CompressionZstd:
		enc, err = newZstdEncoder(frame, e.opts.CompressionConcurrency, e.opts.TargetFrameSize, zstd.EncoderLevel(e.opts.Level))
	case CompressionLZ4:
		enc = newLZ4Encoder(frame, e.opts.Level)
	default:
		return nil, fmt.Errorf("unsupported compression type: %v", e.opts.CompressionType)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create encoder: %w", err)
	}
	frame.enc = enc

	return frame, nil
}

// writeChunk writes uncompressed data chunk into the frame. len(data) is expected to be <= FrameAlignmentSize.
func (e *encoder) writeChunk(frame *frame, data []byte) error {
	for len(data) > 0 {
		// Write out data that fits the current chunk
		written, err := frame.enc.Write(data)
		if err != nil {
			return err
		}
		frame.lenU += written
		data = data[written:]
	}

	// Enforce uncompressed frame size cap.
	maxU := e.opts.MaxUncompressedFrameSize
	if maxU == 0 {
		maxU = DefaultMaxFrameUncompressedSize
	}
	if frame.lenU >= maxU {
		e.mu.Lock()
		frame.flushing = true
		e.mu.Unlock()
	}

	return nil
}

// Write implements io.Writer to be used as the output of the compression encoder.
func (frame *frame) Write(p []byte) (n int, err error) {
	e := frame.e
	n, err = frame.compressedBuffer.Write(p)
	frame.lenC += n

	e.mu.Lock()
	if frame.lenC < e.opts.TargetFrameSize || frame.flushing {
		e.mu.Unlock()

		return n, err
	}
	frame.flushing = true
	e.mu.Unlock()

	return n, err
}

func newZstdEncoder(out io.Writer, concurrency int, windowSize int, compressionLevel zstd.EncoderLevel) (*zstd.Encoder, error) {
	switch {
	case concurrency > 0 && windowSize > 0:
		return zstd.NewWriter(out,
			zstd.WithEncoderConcurrency(concurrency),
			zstd.WithWindowSize(windowSize),
			zstd.WithEncoderLevel(compressionLevel))
	case concurrency > 0:
		return zstd.NewWriter(out,
			zstd.WithEncoderConcurrency(concurrency),
			zstd.WithEncoderLevel(compressionLevel))
	case windowSize > 0:
		return zstd.NewWriter(out,
			zstd.WithWindowSize(windowSize),
			zstd.WithEncoderLevel(compressionLevel))
	default:
		return zstd.NewWriter(out,
			zstd.WithEncoderLevel(compressionLevel))
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
