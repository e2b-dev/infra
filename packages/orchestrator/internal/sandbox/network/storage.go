package network

import (
	"context"
)

type Storage interface {
	Acquire(ctx context.Context) (*Slot, error)
	Release(ctx context.Context, s *Slot) error
}
