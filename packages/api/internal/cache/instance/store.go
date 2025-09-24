package instance

import (
	"context"
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type (
	InsertCallback func(ctx context.Context, sbx Sandbox, created bool)
)

type MemoryStore struct {
	reservations *ReservationCache
	items        cmap.ConcurrentMap[string, *memorySandbox]

	// If the callback isn't very simple, consider running it in a goroutine to prevent blocking the main flow
	insertCallbacks      []InsertCallback
	insertAsyncCallbacks []InsertCallback

	mu sync.Mutex
}

func NewStore(
	insertCallbacks []InsertCallback,
	insertAsyncCallbacks []InsertCallback,
) *MemoryStore {
	instanceCache := &MemoryStore{
		items: cmap.New[*memorySandbox](),

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,

		reservations: NewReservationCache(),
	}

	return instanceCache
}
