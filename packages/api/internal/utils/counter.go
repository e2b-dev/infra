package utils

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	sqlcdb "github.com/e2b-dev/infra/packages/db/client"
	"github.com/e2b-dev/infra/packages/db/queries"
)

type TemplateCounter struct {
	count      int64
	lastUpdate time.Time
}

type TemplateSpawnCounter struct {
	counters map[string]*TemplateCounter
	mu       sync.Mutex
	ticker   *time.Ticker
	done     chan bool
}

func NewTemplateSpawnCounter(ctx context.Context, tickerDuration time.Duration, dbClient *sqlcdb.Client) *TemplateSpawnCounter {
	counter := &TemplateSpawnCounter{
		counters: make(map[string]*TemplateCounter),
		ticker:   time.NewTicker(tickerDuration),
		done:     make(chan bool),
	}

	go counter.processUpdates(ctx, dbClient)
	return counter
}

func (t *TemplateSpawnCounter) IncreaseTemplateSpawnCount(templateID string, time time.Time) {
	t.mu.Lock()
	if _, exists := t.counters[templateID]; !exists {
		t.counters[templateID] = &TemplateCounter{}
	}
	t.counters[templateID].count++
	t.counters[templateID].lastUpdate = time
	t.mu.Unlock()
}

func (t *TemplateSpawnCounter) processUpdates(ctx context.Context, dbClient *sqlcdb.Client) {
	for {
		select {
		case <-t.ticker.C:
			t.flushCounters(ctx, dbClient)
		case <-ctx.Done():
			t.ticker.Stop()
			return
		case <-t.done:
			t.ticker.Stop()

			// Final flush before stopping
			t.flushCounters(ctx, dbClient)
			return
		}
	}
}

func (t *TemplateSpawnCounter) flushCounters(ctx context.Context, dbClient *sqlcdb.Client) {
	t.mu.Lock()
	updates := make(map[string]*TemplateCounter)
	for templateID, counter := range t.counters {
		if counter.count > 0 {
			updates[templateID] = counter
		}
	}
	// Clear the counters
	t.counters = make(map[string]*TemplateCounter)
	t.mu.Unlock()

	for templateID, counter := range updates {
		err := dbClient.UpdateTemplateSpawnCount(ctx, queries.UpdateTemplateSpawnCountParams{
			SpawnCount:    counter.count,
			LastSpawnedAt: &counter.lastUpdate,
			TemplateID:    templateID,
		})
		if err != nil {
			zap.L().Error("error updating template spawn count", zap.Error(err))
		}
	}
}

func (t *TemplateSpawnCounter) Close() {
	select {
	case t.done <- true:
	default:
		zap.L().Debug("template spawn counter already closed")
	}
}
