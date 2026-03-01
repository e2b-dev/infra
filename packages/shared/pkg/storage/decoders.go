package storage

import (
	"context"
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

// --- lz4 pool ---

var lz4Pool sync.Pool

func getLZ4Reader(r io.Reader) *lz4.Reader {
	if v := lz4Pool.Get(); v != nil {
		rd := v.(*lz4.Reader)
		rd.Reset(r)

		return rd
	}

	return lz4.NewReader(r)
}

func putLZ4Reader(rd *lz4.Reader) {
	rd.Reset(nil)
	lz4Pool.Put(rd)
}
