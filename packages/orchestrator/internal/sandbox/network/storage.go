package network

import (
	"context"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

type Storage interface {
	Acquire(ctx context.Context) (*Slot, error)
	Release(ctx context.Context, s *Slot) error
}

// NewStorage creates a new slot storage based on the environment, we are ok with using a memory storage for local
func NewStorage(nodeID string, config Config) (Storage, error) {
	if env.IsDevelopment() || config.UseLocalNamespaceStorage {
		return NewStorageLocal(config)
	}

	return NewStorageKV(nodeID, config)
}
