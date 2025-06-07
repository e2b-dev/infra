package network

import (
	"context"
	"os"

	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
)

var (
	localNamespaceStorageSwitch = os.Getenv("USE_LOCAL_NAMESPACE_STORAGE")
)

type Storage interface {
	Acquire(ctx context.Context) (*Slot, error)
	Release(*Slot) error
}

// NewStorage creates a new slot storage based on the environment, we are ok with using a memory storage for local
func NewStorage(slotsSize int, clientID string, tracer trace.Tracer) (Storage, error) {
	if env.IsDevelopment() || localNamespaceStorageSwitch == "true" {
		return NewStorageLocal(slotsSize, tracer)
	}

	return NewStorageKV(slotsSize, clientID)
}
