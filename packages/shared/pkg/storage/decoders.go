package storage

import (
	"io"
	"sync"
	"sync/atomic"

	"github.com/klauspost/compress/zstd"
	lz4 "github.com/pierrec/lz4/v4"
)

var decoderConcurrency atomic.Int32

func init() {
	decoderConcurrency.Store(1)
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
