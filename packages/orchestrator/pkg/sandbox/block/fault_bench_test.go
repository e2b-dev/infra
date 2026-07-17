//go:build linux

package block

import (
	"fmt"
	"path/filepath"
	"testing"
)

// BenchmarkRunFaultSafeOverhead measures the guard's cost around Cache.ReadAt
// at the worst case (one 4K page) and a typical segment size.
func BenchmarkRunFaultSafeOverhead(b *testing.B) {
	for _, size := range []int64{4 << 10, 256 << 10} {
		cache, err := NewCache(size, 4096, filepath.Join(b.TempDir(), "cache"), true)
		if err != nil {
			b.Fatal(err)
		}
		b.Cleanup(func() { _ = cache.Close() })

		buf := make([]byte, size)

		b.Run(fmt.Sprintf("size=%dKiB/unguarded", size>>10), func(b *testing.B) {
			b.SetBytes(size)
			for range b.N {
				if _, err := cache.ReadAt(buf, 0); err != nil {
					b.Fatal(err)
				}
			}
		})

		b.Run(fmt.Sprintf("size=%dKiB/guarded", size>>10), func(b *testing.B) {
			b.SetBytes(size)
			for range b.N {
				if err := RunFaultSafe(b.Context(), func() error {
					_, readErr := cache.ReadAt(buf, 0)

					return readErr
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
