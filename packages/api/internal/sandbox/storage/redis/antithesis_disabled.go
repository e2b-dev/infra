//go:build !antithesis

package redis

import "context"

func assertLockHeld(context.Context, *storageLock, string) {}
