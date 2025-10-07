package network

import (
	"context"
	"os"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type Storage interface {
	Acquire(ctx context.Context) (*Slot, error)
	Release(ctx context.Context, s *Slot) error
}

// NewStorage creates a new slot storage based on the environment, we are ok with using a memory storage for local
func NewStorage(slotsSize int, nodeID string) (Storage, error) {
	if env.IsDevelopment() || os.Getenv("USE_LOCAL_NAMESPACE_STORAGE") == "true" {
		return NewStorageLocal(slotsSize)
	}

	return NewStorageKV(slotsSize, nodeID)
}
