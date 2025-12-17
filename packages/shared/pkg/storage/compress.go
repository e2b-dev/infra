package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sync/errgroup"
)

// Compressed frame contains 1+ chunks; chunks are aligned to 2MB uncompressed
// size (except maybe the last chunk in file).
const (
	targetFrameCompressedSize = 4 * 1024 * 1024 // 4Mb target compressed frame size
	chunkUncompressedSize     = 2 * 1024 * 1024 // 2Mb uncompressed chunk size
	zstdCompressionLevel      = zstd.SpeedFastest
	zstdDefaultConcurrency    = 0 // use default concurrency settings
)

var DefaultCompressionOptions = &CompressionOptions{
	CompressionType: CompressionZstd,
	ChunkSize:       chunkUncompressedSize,
	TargetFrameSize: targetFrameCompressedSize,
	Level:           int(zstdCompressionLevel),
	Concurrency:     zstdDefaultConcurrency,
}

// MultipartCompressUploadFile compresses the given file and uploads it using multipart upload.
func MultipartCompressUploadFile(ctx context.Context, filePath string, u MultipartUploader, maxConcurrency int, opts *CompressionOptions) (*CompressedInfo, error) {
	file, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	defer file.Close()

	fu, err := newFrameUploader(ctx, u, gcpMultipartUploadPartSize, maxConcurrency)
	if err != nil {
		return nil, fmt.Errorf("failed to create frame handler: %w", err)
	}

	fe, err := newFrameEncoder(opts, fu.handleFrame)
	if err != nil {
		return nil, fmt.Errorf("failed to create framed encoder: %w", err)
	}

	return multipartCompressUploadFile(file, fe, fu, 32*1024)
}

// multipartCompressUploadFile is the testable version, used internally by
// MultipartCompressUploadFile.
func multipartCompressUploadFile(file io.Reader, fe *frameEncoder, fu *frameUploader, bufSize int) (*CompressedInfo, error) {
	var err error
	if bufSize > 0 {
		_, err = io.CopyBuffer(fe, file, make([]byte, bufSize))
	} else {
		_, err = io.Copy(fe, file)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to copy file to framed encoder: %w", err)
	}

	fi, err := fe.Close()
	if err != nil {
		return nil, fmt.Errorf("failed to close framed encoder: %w", err)
	}

	// TODO: what happens to incomplete uploads if this fails?
	//
	// TODO: if we error before complete, we never eg.Wait(); presumably the
	// goroutines will exit when the context is cancelled?
	err = fu.complete()
	if err != nil {
		return nil, fmt.Errorf("failed to upload frames: %w", err)
	}

	return fi, nil
}

type frameEncoder struct {
	opts *CompressionOptions

	handleFrame func(frame []byte, last bool) error

	frameUncompressedSize int64
	bytesInChunk          int
	enc                   io.WriteCloser
	compressedBuffer      *syncBuffer
	info                  *CompressedInfo
}

type frameUploader struct {
	targetPartSize int

	ctx      context.Context
	partN    int
	bytes    int64
	frames   [][]byte
	uploader MultipartUploader
	uploadID string
	eg       *errgroup.Group
}

func newFrameEncoder(opts *CompressionOptions,
	handler func(frame []byte, last bool) error,
) (*frameEncoder, error) {
	fe := &frameEncoder{
		opts:        opts,
		handleFrame: handler,
		info:        &CompressedInfo{CompressionType: opts.CompressionType},
	}

	return fe.startFrame()
}

func (fe *frameEncoder) startFrame() (*frameEncoder, error) {
	var enc io.WriteCloser
	var err error
	fe.bytesInChunk = 0
	fe.frameUncompressedSize = 0

	// Can't reset and reuse because we hand off the bytes to another goroutine.
	fe.compressedBuffer = newSyncBuffer(fe.opts.TargetFrameSize + fe.opts.ChunkSize)

	switch fe.opts.CompressionType {
	case CompressionZstd:
		enc, err = newZstdEncoder(fe.compressedBuffer, fe.opts.Concurrency, fe.opts.TargetFrameSize, zstd.EncoderLevel(fe.opts.Level))
	default:
		return nil, fmt.Errorf("unsupported compression type: %v", fe.opts.CompressionType)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	fe.enc = enc

	return fe, nil
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

func (fe *frameEncoder) Close() (info *CompressedInfo, err error) {
	err = fe.closeFrame(true)
	if err != nil {
		return nil, err
	}

	return fe.info, nil
}

func (fe *frameEncoder) closeFrame(last bool) error {
	if fe.enc != nil {
		if err := fe.enc.Close(); err != nil {
			return fmt.Errorf("failed to close encoder: %w", err)
		}
		fe.enc = nil
	}

	bb := fe.compressedBuffer.Bytes()
	if len(bb) > 0 {
		if fe.handleFrame != nil {
			if err := fe.handleFrame(bb, last); err != nil {
				return fmt.Errorf("failed to handle frame: %w", err)
			}
		}

		fe.info.Frames = append(fe.info.Frames, Frame{
			U: int(fe.frameUncompressedSize),
			C: len(bb),
		})
	}

	if !last {
		if _, err := fe.startFrame(); err != nil {
			return fmt.Errorf("failed to start new frame: %w", err)
		}
	}

	return nil
}

func (fe *frameEncoder) Write(data []byte) (n int, err error) {
	for len(data) > 0 {
		// Write out data that fits the current chunk
		remainInChunk := max(int(fe.opts.ChunkSize-fe.bytesInChunk), 0)
		writeNow := min(len(data), remainInChunk)
		written, err := fe.enc.Write(data[:writeNow])
		n += written
		if err != nil {
			return n, err
		}
		fe.bytesInChunk += written
		fe.frameUncompressedSize += int64(written)
		data = data[written:]

		// See if we reached the end of the chunk
		if fe.bytesInChunk >= fe.opts.ChunkSize {
			// See if the chunk puts us over the target encoded frame size
			if fe.compressedBuffer.Len() >= fe.opts.TargetFrameSize {
				if err := fe.closeFrame(false); err != nil {
					return n, err
				}
			}
			fe.bytesInChunk = 0
		}
	}

	return n, err
}

func newFrameUploader(ctx context.Context, u MultipartUploader, targetPartSize int, maxConcurrency int) (*frameUploader, error) {
	uploadID, err := u.InitiateUpload()
	if err != nil {
		return nil, fmt.Errorf("failed to initiate upload: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(maxConcurrency)

	return &frameUploader{
		ctx:            ctx,
		uploader:       u,
		uploadID:       uploadID,
		eg:             eg,
		targetPartSize: targetPartSize,
		partN:          1,
	}, nil
}

func (u *frameUploader) handleFrame(frame []byte, last bool) error {
	u.bytes += int64(len(frame))
	u.frames = append(u.frames, frame)

	if u.bytes < int64(u.targetPartSize) && !last {
		// Nothing else to do until we have more frames
		return nil
	}

	u.goUploadPart(u.partN, u.frames)
	u.partN++
	u.frames = nil
	u.bytes = 0

	return nil
}

func (u *frameUploader) goUploadPart(n int, frames [][]byte) {
	u.eg.Go(func() error {
		err := u.uploader.UploadPart(u.uploadID, n, frames...)
		if err != nil {
			return fmt.Errorf("failed to upload part %d: %w", n, err)
		}

		return nil
	})
}

func (u *frameUploader) complete() error {
	// Wait for all uploads to complete
	if err := u.eg.Wait(); err != nil {
		return fmt.Errorf("failed to upload frames: %w", err)
	}

	// Complete multipart upload
	if err := u.uploader.CompleteUpload(u.uploadID); err != nil {
		return fmt.Errorf("failed to complete upload: %w", err)
	}

	return nil
}

type vectorReader struct {
	data [][]byte
	pos  int
	off  int
}

func newVectorReader(data [][]byte) *vectorReader {
	return &vectorReader{
		data: data,
	}
}

func (mr *vectorReader) Read(p []byte) (n int, err error) {
	if mr.pos >= len(mr.data) {
		return 0, io.EOF
	}

	n = copy(p, mr.data[mr.pos][mr.off:])
	mr.off += n
	if mr.off >= len(mr.data[mr.pos]) {
		mr.pos++
		mr.off = 0
	}

	return n, nil
}

type syncBuffer struct {
	*bytes.Buffer

	mu *sync.Mutex
}

func newSyncBuffer(size int) *syncBuffer {
	return &syncBuffer{
		Buffer: bytes.NewBuffer(make([]byte, 0, size)),
		mu:     &sync.Mutex{},
	}
}

func (cb *syncBuffer) Write(p []byte) (n int, err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	n, err = cb.Buffer.Write(p)

	return n, err
}

func (cb *syncBuffer) Len() int {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.Buffer.Len()
}

func (cb *syncBuffer) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.Buffer.Reset()
}

func (cb *syncBuffer) Bytes() []byte {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	return cb.Buffer.Bytes()
}

// Iterates over frames that overlap with the given range and calls fn for each frame.
func (ci *CompressedInfo) Range(start, length int64, fn func(offset Offset, frame Frame) error) error {
	var currentOffset Offset
	for _, frame := range ci.Frames {
		frameEnd := currentOffset.U + int64(frame.U)
		requestEnd := start + length
		if frameEnd <= start {
			// frame is before the requested range
			currentOffset.U += int64(frame.U)
			currentOffset.C += int64(frame.C)

			continue
		}
		if currentOffset.U >= requestEnd {
			// frame is after the requested range
			break
		}

		// frame overlaps with the requested range
		if err := fn(currentOffset, frame); err != nil {
			return err
		}
		currentOffset.U += int64(frame.U)
		currentOffset.C += int64(frame.C)
	}

	return nil
}

func (ci *CompressedInfo) TotalUncompressedSize() int64 {
	var total int64
	for _, frame := range ci.Frames {
		total += int64(frame.U)
	}

	return total
}

func (ci *CompressedInfo) TotalCompressedSize() int64 {
	var total int64
	for _, frame := range ci.Frames {
		total += int64(frame.C)
	}

	return total
}
