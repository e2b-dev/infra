package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

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

// decompressReader decompresses on Read, metering raw pulls vs decoded output
// separately so source-read wall and decompression CPU are split. On Close it
// drains the decoder to EOF (so any wrapper below — e.g. captureReader —
// observes the full encoded stream) and emits the orchestrator.read.decompress
// record (decompress time + uncompressed bytes, keyed by file_type/source/codec).
type decompressReader struct {
	inner        RangeReader // retained to call Close;
	meteredIn    *meteredReader
	meteredOut   *meteredReader
	releaseCodec func()
	ct           CompressionType
	source       Source
	objType      SeekableObjectType
	readErr      error
}

// NewDecompressingReader wraps inner so Read returns decompressed bytes;
// metric attribution falls back to defaults (callers that care provide it via
// newDecompressReader directly).
func NewDecompressingReader(inner RangeReader, ct CompressionType) (RangeReader, error) {
	return newDecompressReader(inner, ct, UnknownSource, UnknownSeekableObjectType)
}

func newDecompressReader(inner RangeReader, ct CompressionType, src Source, ot SeekableObjectType) (*decompressReader, error) {
	compressed := &meteredReader{inner: inner}

	var dec io.Reader
	var releaseCodec func()

	switch ct {
	case CompressionLZ4:
		d := getLZ4Decoder(compressed)
		dec, releaseCodec = d, func() { putLZ4Decoder(d) }

	case CompressionZstd:
		d, err := getZstdDecoder(compressed)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd decoder: %w", err)
		}
		dec, releaseCodec = d, func() { putZstdDecoder(d) }

	default:
		return nil, fmt.Errorf("unsupported compression type: %s", ct)
	}

	return &decompressReader{
		inner:        inner,
		meteredIn:    compressed,
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
	// Drain to EOF so any wrapper below observes the full encoded stream. With
	// LZ4 BlockChecksum=true / Checksum=false the 4-byte EndMark is otherwise
	// left unread.
	if r.readErr == nil {
		_, _ = io.Copy(io.Discard, r.meteredOut)
	}

	r.releaseCodec()

	stats := &ReadStats{
		CompressedBytes:   r.meteredIn.bytes,
		UncompressedBytes: r.meteredOut.bytes,
		Read:              time.Duration(r.meteredIn.nanos),
		Decompress:        max(0, time.Duration(r.meteredOut.nanos)-time.Duration(r.meteredIn.nanos)),
	}

	recordDecompressStep(ctx, r, stats, r.readErr)

	_, innerErr := r.inner.Close(ctx)

	return stats, innerErr
}
