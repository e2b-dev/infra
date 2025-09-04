package instance

import (
	"context"
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type (
	InsertCallback func(ctx context.Context, sbx *InstanceInfo, created bool)
	RemoveCallback func(ctx context.Context, sbx *InstanceInfo, removeType RemoveType)
)

type MemoryStore struct {
	reservations *ReservationCache
	items        cmap.ConcurrentMap[string, *InstanceInfo]

	// If the callback isn't very simple, consider running it in a goroutine to prevent blocking the main flow
	insertCallbacks      []InsertCallback
	insertAsyncCallbacks []InsertCallback

	removeSandbox        func(ctx context.Context, sbx *InstanceInfo, removeType RemoveType) error
	removeAsyncCallbacks []RemoveCallback

	mu sync.Mutex
}

func NewStore(
	removeSandbox func(ctx context.Context, sbx *InstanceInfo, removeType RemoveType) error,
	insertCallbacks []InsertCallback,
	insertAsyncCallbacks []InsertCallback,
	removeAsyncCallbacks []RemoveCallback,
) *MemoryStore {
	instanceCache := &MemoryStore{
		items: cmap.New[*InstanceInfo](),

		removeSandbox: removeSandbox,

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,

		removeAsyncCallbacks: removeAsyncCallbacks,
		reservations:         NewReservationCache(),
	}

	return instanceCache
}
