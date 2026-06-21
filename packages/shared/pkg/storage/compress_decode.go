package storage

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"
)

var _ RangeReader = (*decompressReader)(nil)

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
const zstdDecoderConcurrency = 1

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

	return zstd.NewReader(r, zstd.WithDecoderConcurrency(zstdDecoderConcurrency))
}

func putZstdDecoder(dec *zstd.Decoder) {
	dec.Reset(nil)
	zstdDecoderPool.Put(dec)
}

// decompressReader decompresses inner on Read; Close releases the codec back
// to its pool and closes inner.
type decompressReader struct {
	inner        RangeReader
	dec          io.Reader
	releaseCodec func()
}

func NewDecompressingReader(inner RangeReader, ct CompressionType) (RangeReader, error) {
	var dec io.Reader
	var releaseCodec func()

	switch ct {
	case CompressionLZ4:
		d := getLZ4Decoder(inner)
		dec, releaseCodec = d, func() { putLZ4Decoder(d) }

	case CompressionZstd:
		d, err := getZstdDecoder(inner)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		dec, releaseCodec = d, func() { putZstdDecoder(d) }

	default:
		return nil, fmt.Errorf("unsupported compression type: %s", ct)
	}

	return &decompressReader{
		inner:        inner,
		dec:          dec,
		releaseCodec: releaseCodec,
	}, nil
}

func (r *decompressReader) Read(p []byte) (int, error) {
	return r.dec.Read(p)
}

func (r *decompressReader) Close(ctx context.Context) error {
	r.releaseCodec()

	return r.inner.Close(ctx)
}
