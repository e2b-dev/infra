package batcher

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

type Batcher struct {
	db        *db.DB
	templates map[string]int64
	ctx       context.Context

	mu sync.Mutex
}

func NewBatcher(ctx context.Context, db *db.DB) *Batcher {
	return &Batcher{
		db:        db,
		ctx:       ctx,
		templates: make(map[string]int64),
	}
}

// UpdateTemplateSpawnCount updates the spawn count for the given environment.
func (b *Batcher) UpdateTemplateSpawnCount(templateID string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if _, ok := b.templates[templateID]; !ok {
		b.templates[templateID] = 0
	}

	b.templates[templateID]++
}

func (b *Batcher) Loop() {
	ticker := time.NewTicker(time.Minute)
	for {
		select {
		case <-ticker.C:
			b.batch()
		case <-b.ctx.Done():
			ticker.Stop()
			return
		}
	}
}

func (b *Batcher) batch() {
	b.mu.Lock()
	for env, count := range b.templates {
		if count == 0 {
			continue
		}

		err := b.db.Client.Env.UpdateOneID(env).AddSpawnCount(count).Exec(b.ctx)
		if err != nil {
			log.Printf("failed to update spawn count for env %s: %v", env, err)
		}

		delete(b.templates, env)
	}
}
