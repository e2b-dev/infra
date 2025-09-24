package memory

import (
	"context"
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

type (
	InsertCallback func(ctx context.Context, sbx sandbox.Sandbox, created bool)
)

type Store struct {
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
) *Store {
	instanceCache := &Store{
		items: cmap.New[*memorySandbox](),

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,

		reservations: NewReservationCache(),
	}

	return instanceCache
}
