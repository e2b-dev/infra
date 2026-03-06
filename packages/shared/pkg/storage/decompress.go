package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"

	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
)

var decoderConcurrency atomic.Int32

func init() {
	decoderConcurrency.Store(1)
}

// InitDecoders reads the compress-config feature flag and sets the pooled
// zstd decoder concurrency. Call once at startup before any reads.
//
// TODO: decoderConcurrency is set once at startup and not re-evaluated.
// Move to core orchestrator config or re-read periodically.
func InitDecoders(ctx context.Context, ff *featureflags.Client) {
	v := ff.JSONFlag(ctx, featureflags.CompressConfigFlag).AsValueMap()
	n := max(v.Get("decoderConcurrency").IntValue(), 1)
	SetDecoderConcurrency(n)
}

// SetDecoderConcurrency sets the number of concurrent goroutines used by
// pooled zstd decoders. Call from orchestrator startup before any reads.
func SetDecoderConcurrency(n int) {
	if n < 1 {
		n = 1
	}
	decoderConcurrency.Store(int32(n))
}

// --- zstd pool ---

var zstdPool sync.Pool

func getZstdDecoder(r io.Reader) (*zstd.Decoder, error) {
	if v := zstdPool.Get(); v != nil {
		dec := v.(*zstd.Decoder)
		if err := dec.Reset(r); err != nil {
			dec.Close()

			return nil, err
		}

		return dec, nil
	}

	dec, err := zstd.NewReader(r,
		zstd.WithDecoderConcurrency(int(decoderConcurrency.Load())),
	)
	if err != nil {
		return nil, err
	}

	return dec, nil
}

func putZstdDecoder(dec *zstd.Decoder) {
	dec.Reset(nil)
	zstdPool.Put(dec)
}

// --- Decompress functions ---

// DecompressLZ4 decompresses LZ4-block-compressed src into dst and returns
// the decompressed slice (dst[:n]). dst must be large enough for the output.
func DecompressLZ4(src, dst []byte) ([]byte, error) {
	n, err := lz4.UncompressBlock(src, dst)
	if err != nil {
		return nil, fmt.Errorf("lz4 block decompress: %w", err)
	}

	return dst[:n], nil
}

// DecompressReader decompresses from r into a new buffer of uncompressedSize.
func DecompressReader(ct CompressionType, r io.Reader, uncompressedSize int) ([]byte, error) {
	switch ct {
	case CompressionZstd:
		buf := make([]byte, uncompressedSize)
		dec, err := getZstdDecoder(r)
		if err != nil {
			return nil, fmt.Errorf("failed to create zstd reader: %w", err)
		}
		defer putZstdDecoder(dec)

		n, err := io.ReadFull(dec, buf)
		if err != nil {
			return nil, fmt.Errorf("zstd decompress: %w", err)
		}

		return buf[:n], nil

	case CompressionLZ4:
		compressed, err := io.ReadAll(r)
		if err != nil {
			return nil, fmt.Errorf("lz4 read compressed: %w", err)
		}
		buf := make([]byte, uncompressedSize)

		out, err := DecompressLZ4(compressed, buf)
		if err != nil {
			return nil, err
		}
		if len(out) != uncompressedSize {
			return nil, fmt.Errorf("lz4 decompress: expected %d bytes, got %d", uncompressedSize, len(out))
		}

		return out, nil

	default:
		return nil, fmt.Errorf("unsupported compression type: %d", ct)
	}
}

// DecompressFrame decompresses an in-memory compressed byte slice.
func DecompressFrame(ct CompressionType, compressed []byte, uncompressedSize int32) ([]byte, error) {
	switch ct {
	case CompressionLZ4:
		buf := make([]byte, uncompressedSize)

		out, err := DecompressLZ4(compressed, buf)
		if err != nil {
			return nil, err
		}
		if len(out) != int(uncompressedSize) {
			return nil, fmt.Errorf("lz4 decompress: expected %d bytes, got %d", uncompressedSize, len(out))
		}

		return out, nil
	default:
		return DecompressReader(ct, bytes.NewReader(compressed), int(uncompressedSize))
	}
}
