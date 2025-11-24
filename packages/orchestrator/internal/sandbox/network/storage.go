package network

import (
	"context"
)

type Storage interface {
	Setup(ctx context.Context) error
	Acquire(ctx context.Context) (*Slot, error)
	Release(s *Slot) error
}
