package compress

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/klauspost/compress/zstd"
)

// SeekableReader provides random-access reads over compressed data.
type SeekableReader interface {
	ReadAt(p []byte, off int64) (n int, err error)
	Seek(offset int64, whence int) (int64, error)
}

// FrameInfo describes one compressed frame's location in the output.
type FrameInfo struct {
	Index            uint32 // frame index (= uncompressed_offset / FrameSize)
	CompressedOffset uint64
	CompressedSize   uint32
	UncompressedSize uint32
}

// CompressData splits src into independently-compressed frames and writes them
// to dst. Returns the frame table so the caller can store it in a header.
func CompressData(ctx context.Context, src io.Reader, dst io.Writer, cfg *Config) ([]FrameInfo, error) {
	if cfg == nil {
		return nil, fmt.Errorf("compress: config is nil")
	}

	codec, err := newZstdCodec(cfg)
	if err != nil {
		return nil, err
	}

	frameSrc := func() ([]byte, error) {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		buf := make([]byte, FrameSize)
		n, err := io.ReadFull(src, buf)
		if n == 0 {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return nil, nil
			}
			return nil, err
		}
		if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
			return nil, err
		}
		return buf[:n], nil
	}

	entries, err := writeFrames(dst, codec, frameSrc, cfg.concurrency())
	if err != nil {
		return nil, err
	}

	frames := make([]FrameInfo, len(entries))
	for i, e := range entries {
		frames[i] = FrameInfo{
			Index:            uint32(i),
			CompressedOffset: e.compOff,
			CompressedSize:   uint32(e.compSize),
			UncompressedSize: uint32(e.uncompSz),
		}
	}
	return frames, nil
}

// NewReaderFromFrames creates a SeekableReader using an externally-provided
// frame table (e.g. from the build header). The src should contain the raw
// compressed frames only (no footer).
func NewReaderFromFrames(src io.ReaderAt, frames []FrameInfo) (SeekableReader, error) {
	codec, err := newZstdCodec(DefaultConfig())
	if err != nil {
		return nil, err
	}

	entries := make([]entry, len(frames))
	for i, f := range frames {
		entries[i] = entry{
			compOff:   f.CompressedOffset,
			compSize:  uint64(f.CompressedSize),
			uncompOff: uint64(f.Index) * uint64(FrameSize),
			uncompSz:  uint64(f.UncompressedSize),
		}
	}

	var size int64
	if len(entries) > 0 {
		last := entries[len(entries)-1]
		size = int64(last.uncompOff) + int64(last.uncompSz)
	}

	return &seekableReader{
		src:     src,
		codec:   codec,
		entries: entries,
		size:    size,
	}, nil
}

type zstdCodec struct {
	enc *zstd.Encoder
	dec *zstd.Decoder
}

func newZstdCodec(cfg *Config) (*zstdCodec, error) {
	enc, err := zstd.NewWriter(nil, zstd.WithEncoderLevel(zstd.EncoderLevelFromZstd(cfg.level())))
	if err != nil {
		return nil, err
	}
	dec, err := zstd.NewReader(nil)
	if err != nil {
		enc.Close()
		return nil, err
	}
	return &zstdCodec{enc: enc, dec: dec}, nil
}

func (c *zstdCodec) CompressBlock(src []byte) ([]byte, error) {
	return c.enc.EncodeAll(src, make([]byte, 0, len(src))), nil
}

func (c *zstdCodec) DecompressBlock(src []byte, uncompressedSize int) ([]byte, error) {
	return c.dec.DecodeAll(src, make([]byte, 0, uncompressedSize))
}
