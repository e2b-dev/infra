package memory

import (
	"sync"

	cmap "github.com/orcaman/concurrent-map/v2"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox/store"
)

type Backend struct {
	reservations *ReservationStore
	items        cmap.ConcurrentMap[string, *sandbox]

	mu sync.Mutex
}

var _ store.Backend = &Backend{}

func New() *Backend {
	return &Backend{
		items: cmap.New[*sandbox](),

		reservations: NewReservationCache(),
	}
}
