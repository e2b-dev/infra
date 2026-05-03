package block

import "bytes"

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
