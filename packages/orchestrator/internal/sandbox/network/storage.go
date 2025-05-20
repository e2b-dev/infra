package network

import "go.opentelemetry.io/otel/trace"

type Storage interface {
	Acquire() (*Slot, error)
	Release(*Slot) error
}

// NewStorage creates a new slot storage based on the environment, we are ok with using a memory storage for local
func NewStorage(slotsSize int, clientID string, tracer trace.Tracer) (Storage, error) {
	return NewStorageLocal(slotsSize, tracer)
}
