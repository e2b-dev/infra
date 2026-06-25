package storage

import (
	"context"
	"errors"
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

// decompressReader meters raw source pulls (meteredIn) separately from decoded
// output (meteredOut) so read.decompress can split source-read wall from
// decompression CPU on Close.
type decompressReader struct {
	inner        RangeReader
	meteredIn    *meteredReader
	meteredOut   *meteredReader
	releaseCodec func()
	ct           CompressionType
	source       Source
	objType      SeekableObjectType
	readErr      error
}

// NewDecompressReader wraps inner with a decoder for ct, attributing
// read.decompress to src/ot. Callers without that context (tests) pass
// UnknownSource / UnknownSeekableObjectType.
func NewDecompressReader(inner RangeReader, ct CompressionType, src Source, ot SeekableObjectType) (RangeReader, error) {
	metered := &meteredReader{inner: inner}

	var dec io.Reader
	var releaseCodec func()

	switch ct {
	case CompressionLZ4:
		d := getLZ4Decoder(metered)
		dec, releaseCodec = d, func() { putLZ4Decoder(d) }

	case CompressionZstd:
		d, err := getZstdDecoder(metered)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		dec, releaseCodec = d, func() { putZstdDecoder(d) }

	default:
		return nil, fmt.Errorf("unsupported compression type: %s", ct)
	}

	return &decompressReader{
		inner:        inner,
		meteredIn:    metered,
		meteredOut:   &meteredReader{inner: dec},
		releaseCodec: releaseCodec,
		ct:           ct,
		source:       src,
		objType:      ot,
	}, nil
}

func (r *decompressReader) Read(p []byte) (int, error) {
	n, err := r.meteredOut.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.readErr = err
	}

	return n, err
}

func (r *decompressReader) Close(ctx context.Context) (*ReadStats, error) {
	r.releaseCodec()

	stats := &ReadStats{
		StoredBytes:    r.meteredIn.bytes,
		DeliveredBytes: r.meteredOut.bytes,
		Read:           r.meteredIn.read,
		Decompress:     max(0, r.meteredOut.read-r.meteredIn.read),
	}

	recordDecompressStep(ctx, r, stats, r.readErr)

	_, innerErr := r.inner.Close(ctx)

	return stats, innerErr
}
