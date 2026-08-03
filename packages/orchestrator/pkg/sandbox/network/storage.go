//go:build linux

package network

import (
	"context"
)

type Storage interface {
	Acquire(ctx context.Context) (*Slot, error)
	// Release frees the slot. Remote-backed implementations must honor ctx so
	// shutdown can cut a call to an unresponsive backend short.
	Release(ctx context.Context, s *Slot) error
}
