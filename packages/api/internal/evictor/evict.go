package evictor

import (
	"context"
	"time"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
)

type Evictor struct {
	store      *instance.MemoryStore
	removeItem func(ctx context.Context, item *instance.InstanceInfo, kill bool) error
}

func New(store *instance.MemoryStore) *Evictor {
	return &Evictor{store: store}
}

func (e *Evictor) Start(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			// Get all items from the cache before iterating over them
			// to avoid holding the lock while removing items from the cache.
			items := e.store.Items(nil)

			for _, item := range items {
				if item.IsExpired() {
					go func() {
						if err := e.removeItem(ctx, item, false); err != nil {
							// TODO:
							// Log the error but continue processing other items
							// as this is a best-effort eviction.
							// The item will be retried on the next iteration.
							// Use a logger here if available in your context.
						}
					}()
				}
			}
		}
	}
}
