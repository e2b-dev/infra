package storage

import (
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"
)

var lz4DecoderPool sync.Pool

func getLZ4Decoder(r io.Reader) *lz4.Reader {
	if v := lz4DecoderPool.Get(); v != nil {
		dec := v.(*lz4.Reader)
		dec.Reset(r)

		return dec
	}

	return lz4.NewReader(r)
}

func putLZ4Decoder(dec *lz4.Reader) {
	dec.Reset(nil)
	lz4DecoderPool.Put(dec)
}

// zstd concurrency is hardcoded to 1: benchmarks show higher values hurt
// throughput for single 2MiB frame decodes.
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

	return zstd.NewReader(r)
}

func putZstdDecoder(dec *zstd.Decoder) {
	dec.Reset(nil)
	zstdDecoderPool.Put(dec)
}

// NewDecompressingReader wraps a reader with the appropriate decompressor.
// Close releases the decompressor back to its pool but does NOT close the
// underlying reader — the caller is responsible for closing it.
func NewDecompressingReader(raw io.Reader, ct CompressionType) (io.ReadCloser, error) {
	switch ct {
	case CompressionLZ4:
		dec := getLZ4Decoder(raw)

		return &pooledDecoder{
			Reader: dec,
			close:  func() { putLZ4Decoder(dec) },
		}, nil

	case CompressionZstd:
		dec, err := getZstdDecoder(raw)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}

		return &pooledDecoder{
			Reader: dec,
			close:  func() { putZstdDecoder(dec) },
		}, nil

	default:
		return nil, fmt.Errorf("unsupported compression type: %s", ct)
	}
}

// pooledDecoder wraps a decompressor from a sync.Pool.
// Close returns the decompressor to the pool.
type pooledDecoder struct {
	io.Reader

	close func()
}

func (r *pooledDecoder) Close() error {
	r.close()

	return nil
}

// newDecompressingReadCloser wraps raw with the appropriate decompressor and
// takes ownership: Close releases the decompressor back to the pool AND closes raw.
func newDecompressingReadCloser(raw io.ReadCloser, ct CompressionType) (io.ReadCloser, error) {
	dec, err := NewDecompressingReader(raw, ct)
	if err != nil {
		return nil, err
	}

	return &decompressingReadCloser{dec: dec, raw: raw}, nil
}

// decompressingReadCloser reads from the decompressor and closes both the
// decompressor (returning it to the pool) and the underlying raw stream.
type decompressingReadCloser struct {
	dec io.ReadCloser // decompressor — reads from raw
	raw io.Closer     // underlying stream
}

func (c *decompressingReadCloser) Read(p []byte) (int, error) {
	return c.dec.Read(p)
}

func (c *decompressingReadCloser) Close() error {
	decErr := c.dec.Close()
	rawErr := c.raw.Close()

	if decErr != nil {
		return decErr
	}

	return rawErr
}
