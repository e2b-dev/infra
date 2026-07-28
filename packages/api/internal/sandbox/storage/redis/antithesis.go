//go:build antithesis

// Antithesis properties for the sandbox storage. Only built with the
// `antithesis` tag, so production builds carry neither the SDK nor the extra
// Redis round trips.
package redis

import (
	"context"

	"github.com/antithesishq/antithesis-sdk-go/assert"
)

// assertLockHeld checks the distributed lock still has time left at the moment
// we commit a sandbox mutation. Eviction and timeout extension both
// read-modify-write the same record under this lock and write back the value
// they read; if the lock expires mid-section they can interleave and one
// silently overwrites the other, evicting a sandbox whose deadline was just
// extended.
func assertLockHeld(ctx context.Context, lock *storageLock, op string) {
	ttl, err := lock.TTL(ctx)

	assert.Always(err == nil && ttl > 0, "Sandbox state writes hold the lock", map[string]any{
		"op":     op,
		"ttl_ms": ttl.Milliseconds(),
		"error":  err,
	})
}
