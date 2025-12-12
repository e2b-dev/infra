package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"

	"github.com/klauspost/compress/zstd"
	"golang.org/x/sync/errgroup"
)

// Compressed frame contains 1+ chunks; chunks are aligned to 2MB uncompressed
// size (except maybe the last chunk in file).
const (
	targetFrameCompressedSize  = 4 * 1024 * 1024 // 4Mb target compressed frame size
	frameChunkUncompressedSize = 2 * 1024 * 1024 // 2Mb uncompressed chunks size
)

// MultipartCompressUploadFile compresses the given file and uploads it using multipart upload.
func MultipartCompressUploadFile(ctx context.Context, file *os.File, u MultipartUploader, maxConcurrency int, compression CompressionType) error {
	fh, err := newEncodedFrameHandler(ctx, u, maxConcurrency)
	if err != nil {
		return fmt.Errorf("failed to create frame handler: %w", err)
	}

	fe, err := newFramedEncoder(compression, fh.handleFrame)
	if err != nil {
		return fmt.Errorf("failed to create framed encoder: %w", err)
	}
	defer fe.Close()

	// Copy file data to framed encoder which is already set up to upload frames
	// as they are created.
	_, err = io.Copy(fe, file)
	if err != nil {
		return fmt.Errorf("failed to copy file to framed encoder: %w", err)
	}

	err = fh.complete()
	if err != nil {
		return fmt.Errorf("failed to upload frames: %w", err)
	}

	// TODO: <>/<> fh accumulated the seek table during frame uploads
	return nil
}

type framedEncoder struct {
	origSize int64

	compression     CompressionType
	chunkAlignment  int
	targetFrameSize int
	onFrameReady    func(frame []byte) error

	bytesInChunk     int
	enc              io.WriteCloser
	compressedBuffer *bytes.Buffer
}

func newFramedEncoder(compression CompressionType, handler func(frame []byte) error) (*framedEncoder, error) {
	// big enough to fit the target compressed frame and 1 more chunk
	buf := bytes.NewBuffer(
		make([]byte, 0, targetFrameCompressedSize+gcpMultipartUploadPartSize))

	fe := &framedEncoder{
		compression:      compression,
		targetFrameSize:  targetFrameCompressedSize,
		chunkAlignment:   frameChunkUncompressedSize,
		compressedBuffer: buf,
		onFrameReady:     handler,
	}

	return fe.startFrame()
}

func (fe *framedEncoder) Close() error {
	if fe.enc != nil {
		if err := fe.enc.Close(); err != nil {
			return fmt.Errorf("failed to close encoder: %w", err)
		}
		fe.enc = nil
	}

	// Final frame
	if fe.onFrameReady != nil && fe.compressedBuffer.Len() > 0 {
		if err := fe.onFrameReady(fe.compressedBuffer.Bytes()); err != nil {
			return fmt.Errorf("failed to handle final frame: %w", err)
		}
		fe.compressedBuffer.Reset()
	}

	fe.bytesInChunk = 0

	return nil
}

func (fe *framedEncoder) startFrame() (*framedEncoder, error) {
	if fe.enc != nil {
		if err := fe.enc.Close(); err != nil {
			return nil, fmt.Errorf("failed to close previous encoder: %w", err)
		}
	}

	var enc io.WriteCloser
	var err error
	switch fe.compression {
	case CompressionZstd:
		enc, err = zstd.NewWriter(fe.compressedBuffer,
			zstd.WithEncoderLevel(zstd.SpeedBestCompression))
	default:
		return nil, fmt.Errorf("unsupported compression type: %v", fe.compression)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to create zstd encoder: %w", err)
	}

	fe.enc = enc
	fe.bytesInChunk = 0
	fe.compressedBuffer.Reset()

	return fe, nil
}

func (fe *framedEncoder) Write(data []byte) (n int, err error) {
	for len(data) > 0 {
		// Write out data that fits the current chunk
		remainInChunk := max(int(fe.chunkAlignment-fe.bytesInChunk), 0)
		writeNow := min(len(data), remainInChunk)
		written, err := fe.enc.Write(data[:writeNow])
		n += written
		if err != nil {
			return n, err
		}
		fe.bytesInChunk += written
		data = data[writeNow:]

		// See if we reached the end of the chunk
		if fe.bytesInChunk >= fe.chunkAlignment {
			// See if the chunk puts us over the target encoded frame size
			if fe.compressedBuffer.Len() >= fe.targetFrameSize {
				if fe.onFrameReady != nil {
					err = fe.onFrameReady(fe.compressedBuffer.Bytes())
					if err != nil {
						return n, err
					}
				}

				if _, err := fe.startFrame(); err != nil {
					return n, err
				}
			}
			fe.bytesInChunk = 0
		}
	}

	return n, err
}

type encodedFrameHandler struct {
	ctx      context.Context
	partN    int
	bytes    int64
	frames   [][]byte
	uploader MultipartUploader
	uploadID string
	eg       *errgroup.Group
}

func newEncodedFrameHandler(ctx context.Context, u MultipartUploader, maxConcurrency int) (*encodedFrameHandler, error) {
	uploadID, err := u.InitiateUpload()
	if err != nil {
		return nil, fmt.Errorf("failed to initiate upload: %w", err)
	}

	eg, ctx := errgroup.WithContext(ctx)
	eg.SetLimit(maxConcurrency)

	return &encodedFrameHandler{
		ctx:      ctx,
		uploader: u,
		uploadID: uploadID,
		eg:       eg,
	}, nil
}

func (h *encodedFrameHandler) handleFrame(frame []byte) error {
	h.bytes += int64(len(frame))
	h.frames = append(h.frames, frame)

	if h.bytes < gcpMultipartUploadPartSize {
		// Nothing else to do until we have more frames
		return nil
	}

	h.goUploadPart(h.partN, h.frames)
	h.partN++
	h.frames = nil
	h.bytes = 0

	return nil
}

func (h *encodedFrameHandler) goUploadPart(n int, frames [][]byte) {
	h.eg.Go(func() error {
		err := h.uploader.UploadPart(h.uploadID, n, frames...)
		if err != nil {
			return fmt.Errorf("failed to upload part %d: %w", n, err)
		}

		return nil
	})
}

func (h *encodedFrameHandler) complete() error {
	// Upload any remaining frames as the last part
	if len(h.frames) > 0 {
		h.goUploadPart(h.partN, h.frames)
		h.partN++
		h.frames = nil
		h.bytes = 0
	}

	// Wait for all uploads to complete
	if err := h.eg.Wait(); err != nil {
		return fmt.Errorf("failed to upload frames: %w", err)
	}

	// Complete multipart upload
	if err := h.uploader.CompleteUpload(h.uploadID); err != nil {
		return fmt.Errorf("failed to complete upload: %w", err)
	}

	return nil
}

func newMultiReader(dataList [][]byte) io.Reader {
	readers := make([]io.Reader, len(dataList))
	for i, data := range dataList {
		readers[i] = bytes.NewReader(data)
	}

	return io.MultiReader(readers...)
}
