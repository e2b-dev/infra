package header

import (
	"bytes"
	"fmt"

	"go.opentelemetry.io/otel"
)

const (
	PageSize        = 4 << 10 // 4 KiB
	HugepageSize    = 2 << 20 // 2 MiB
	RootfsBlockSize = 4 << 10 // 4 KiB
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/shared/pkg/storage/header")

// EmptyHugePage is a zero-filled buffer used as the "build payload" returned
// by Build.ReadAt for nil-build mappings (uuid.Nil). Kept here so callers
// don't allocate per-read. Not used for zero-detection — use IsZero instead.
var EmptyHugePage = make([]byte, HugepageSize)

// IsZero reports whether b is all-zero. The 3-byte sample (first/last/middle)
// rejects most non-zero buffers from a single cache line — a trick lifted from
// QEMU's buffer_is_zero. The fallback uses bytes.Equal on b shifted by one;
// b is all-zero iff b[1:] == b[:len(b)-1] (and b[0] is zero), which dispatches
// to the runtime's SIMD-accelerated memequal on amd64/arm64.
func IsZero(b []byte) bool {
	n := len(b)
	if n == 0 {
		return true
	}
	if b[0]|b[n-1]|b[n/2] != 0 {
		return false
	}
	if n <= 3 {
		return true
	}

	return bytes.Equal(b[:n-1], b[1:])
}

// IsEmptyBlock reports whether a block of blockSize bytes is all-zero.
// blockSize is validated against the block sizes the snapshot format
// supports so callers can distinguish "block is non-empty" from "I gave
// you a buffer of the wrong shape".
func IsEmptyBlock(block []byte, blockSize int64) (bool, error) {
	if blockSize != HugepageSize && blockSize != RootfsBlockSize {
		return false, fmt.Errorf("block size not supported: %d", blockSize)
	}
	if int64(len(block)) != blockSize {
		return false, fmt.Errorf("block length %d != block size %d", len(block), blockSize)
	}

	return IsZero(block), nil
}
