package storage

import (
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"
)

// --- Encoder pool (per-stream) ---

// frameCompressor compresses individual frames. Implementations are pooled
// and reused across frames within a single CompressStream call.
type frameCompressor interface {
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
// Stateless — each call allocates a fresh destination buffer.
type lz4FrameCompressor struct{}

func (l *lz4FrameCompressor) Compress(src []byte) ([]byte, error) {
	dst := make([]byte, lz4.CompressBlockBound(len(src)))

	n, err := lz4.CompressBlock(src, dst, nil)
	if err != nil {
		return nil, fmt.Errorf("lz4 block compress: %w", err)
	}

	if n == 0 {
		return nil, fmt.Errorf("lz4 block compress: incompressible data (%d bytes)", len(src))
	}

	return dst[:n], nil
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
		// LZ4 block compression is stateless — no pool needed.
		return func() (frameCompressor, error) {
				return &lz4FrameCompressor{}, nil
			}, func(frameCompressor) {
				// nothing to return
			}
	}
}

// --- Encoder creation ---

// newZstdEncoder creates a zstd encoder for use with EncodeAll.
// The encoder is created with a nil writer since EncodeAll doesn't use streaming output.
func newZstdEncoder(concurrency int, windowSize int, compressionLevel zstd.EncoderLevel) (*zstd.Encoder, error) {
	zstdOpts := []zstd.EOption{
		zstd.WithEncoderLevel(compressionLevel),
		zstd.WithEncoderCRC(true), // per-frame xxHash64 checksum (default true, explicit for clarity)
	}
	if windowSize > 0 {
		zstdOpts = append(zstdOpts, zstd.WithWindowSize(windowSize))
	}
	if concurrency > 0 {
		zstdOpts = append(zstdOpts, zstd.WithEncoderConcurrency(concurrency))
	}

	return zstd.NewWriter(nil, zstdOpts...)
}

// --- Decoder pool (global) ---

// zstd decoders are expensive to create (~360ns + 7 allocs) and safe to reuse
// via Reset, so we keep a global pool. Concurrency is hardcoded to 1: benchmarks
// show higher values hurt throughput for single 2MiB frame decodes.
var zstdDecoderPool sync.Pool

func getZstdDecoder(r io.Reader) (*zstd.Decoder, error) {
	if v := zstdDecoderPool.Get(); v != nil {
		dec := v.(*zstd.Decoder)
		if err := dec.Reset(r); err != nil {
			dec.Close()

			return nil, err
		}

		return dec, nil
	}

	dec, err := zstd.NewReader(r,
		zstd.WithDecoderConcurrency(1),
	)
	if err != nil {
		return nil, err
	}

	return dec, nil
}

func putZstdDecoder(dec *zstd.Decoder) {
	dec.Reset(nil)
	zstdDecoderPool.Put(dec)
}

func DecompressLZ4(src, dst []byte) ([]byte, error) {
	n, err := lz4.UncompressBlock(src, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 block decompress: %w", err)
	}

	return dst[:n], nil
}

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
