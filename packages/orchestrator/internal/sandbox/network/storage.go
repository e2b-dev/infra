package network

import "github.com/e2b-dev/infra/packages/shared/pkg/env"

type Storage interface {
	Acquire() (*Slot, error)
	Release(*Slot) error
}

// NewStorage creates a new slot storage based on the environment, we are ok with using a memory storage for local
func NewStorage(slotsSize int) (Storage, error) {
	if env.IsLocal() {
		return NewStorageMemory(slotsSize)
	} else {
		return NewStorageKV(slotsSize)
	}
}
