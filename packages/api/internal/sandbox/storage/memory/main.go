package memory

import (
	cmap "github.com/orcaman/concurrent-map/v2"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
)

var _ sandbox.Storage = (*Storage)(nil)

type Storage struct {
	items cmap.ConcurrentMap[string, *memorySandbox]
}

func NewStorage() *Storage {
	instanceCache := &Storage{
		items: cmap.New[*memorySandbox](),
	}

	return instanceCache
}
