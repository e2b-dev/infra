package memory

import (
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

var _ sandbox.Store = (*Store)(nil)

type Store struct {
	reservations *ReservationStorage
	items        cmap.ConcurrentMap[string, *memorySandbox]

	// If the callback isn't very simple, consider running it in a goroutine to prevent blocking the main flow
	insertCallbacks      []sandbox.InsertCallback
	insertAsyncCallbacks []sandbox.InsertCallback

	mu sync.Mutex
}

func NewStore(
	insertCallbacks []sandbox.InsertCallback,
	insertAsyncCallbacks []sandbox.InsertCallback,
) *Store {
	instanceCache := &Store{
		items: cmap.New[*memorySandbox](),

		insertCallbacks:      insertCallbacks,
		insertAsyncCallbacks: insertAsyncCallbacks,

		reservations: NewReservationStorage(),
	}

	return instanceCache
}
