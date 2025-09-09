package store

import (
	"context"
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"
)

type (
	InsertCallback func(ctx context.Context, sbx *Sandbox, created bool)
	RemoveCallback func(ctx context.Context, sbx *Sandbox, removeType RemoveType)
)

type MemoryStore struct {
	reservations *ReservationStore
	items        cmap.ConcurrentMap[string, *Sandbox]

	// If the callback isn't very simple, consider running it in a goroutine to prevent blocking the main flow
	insertCallbacks      []InsertCallback
	insertAsyncCallbacks []InsertCallback

	removeSandbox        func(ctx context.Context, sbx *Sandbox, removeType RemoveType) error
	removeAsyncCallbacks []RemoveCallback

	mu sync.Mutex
}

func NewStore(
	removeSandbox func(ctx context.Context, sbx *Sandbox, removeType RemoveType) error,
	insertCallbacks []InsertCallback,
	insertAsyncCallbacks []InsertCallback,
	removeAsyncCallbacks []RemoveCallback,
) *MemoryStore {
	return &MemoryStore{
		items: cmap.New[*Sandbox](),

		removeSandbox: removeSandbox,

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,

		removeAsyncCallbacks: removeAsyncCallbacks,
		reservations:         NewReservationCache(),
	}
}
