//go:build linux

package network

import (
	"context"
)

type Storage interface {
	Acquire(ctx context.Context) (*Slot, error)
	// Release frees the slot. Implementations backed by a remote store must
	// honor ctx so a shutdown can cut the call short instead of blocking on an
	// unresponsive backend.
	Release(ctx context.Context, s *Slot) error
}
