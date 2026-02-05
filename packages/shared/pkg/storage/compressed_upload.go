package storage

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sync/errgroup"
)

func (c CompressionType) String() string {
	switch c {
	case CompressionNone:
		return "none"
	case CompressionZstd:
		return "zstd"
	case CompressionLZ4:
		return "lz4"
	default:
		return "unknown"
	}
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
	uploader       MultipartUploader
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

func newFrameEncoder(opts *FramedUploadOptions, u MultipartUploader, targetPartSize int64, maxUploadConcurrency int) *encoder {
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
		return nil, fmt.Errorf("failed to start multipart upload: %w", err)
	}

	// Start copying file to the compression encoder. Use a return channel
	// instead of errgroup to be able to detect completion in the event loop.
	// Buffer 8 chunks to allow read-ahead and better pipelining.
	chunkCh := make(chan []byte, 8)
	readErrorCh := make(chan error, 1)
	go e.readFile(ctx, in, e.opts.ChunkSize, chunkCh, readErrorCh)

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
	var err error
	for i := 0; err == nil; i++ {
		var chunk []byte
		chunk, err = readChunk(in, chunkSize)

		if err == nil {
			err = ctx.Err()
		}
		switch {
		case err == nil:
			chunkCh <- chunk
		case errors.Is(err, io.EOF):
			if len(chunk) > 0 {
				chunkCh <- chunk
			}
			close(chunkCh)
		default:
			errorCh <- fmt.Errorf("failed to read file chunk %d: %w", i, err)
		}
	}
}

func readChunk(file io.Reader, chunkSize int) ([]byte, error) {
	chunk := make([]byte, chunkSize)
	var n int
	var err error

	for n < chunkSize && err == nil {
		var c int
		c, err = file.Read(chunk[n:])
		n += c
	}

	return chunk[:n], err
}

func (e *encoder) startFrame() (*frame, error) {
	var enc io.WriteCloser
	var err error
	frame := &frame{
		e:                e,
		compressedBuffer: bytes.NewBuffer(make([]byte, 0, e.opts.TargetFrameSize+e.opts.ChunkSize)),
	}
	switch e.opts.CompressionType {
	case CompressionZstd:
		enc, err = newZstdEncoder(frame, e.opts.CompressionConcurrency, e.opts.TargetFrameSize, zstd.EncoderLevel(e.opts.Level))
	default:
		return nil, fmt.Errorf("unsupported compression type: %v", e.opts.CompressionType)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}
	frame.enc = enc

	return frame, nil
}

// addChunk writes uncompressed data chunk into the frame. len(data) is expected to be <= opts.ChunkSize.
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
