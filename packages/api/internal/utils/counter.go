package utils

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

type TemplateCounter struct {
	count      int
	lastUpdate time.Time
}

type TemplateSpawnCounter struct {
	counters map[string]*TemplateCounter
	mu       sync.Mutex
	ticker   *time.Ticker
	done     chan bool
}

func NewTemplateSpawnCounter(ctx context.Context, tickerDuration time.Duration, dbClient *db.DB) *TemplateSpawnCounter {
	counter := &TemplateSpawnCounter{
		counters: make(map[string]*TemplateCounter),
		ticker:   time.NewTicker(tickerDuration),
		done:     make(chan bool),
	}

	go counter.processUpdates(context.WithoutCancel(ctx), dbClient)
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

func (t *TemplateSpawnCounter) processUpdates(ctx context.Context, dbClient *db.DB) {
	for {
		select {
		case <-t.ticker.C:
			t.flushCounters(ctx, dbClient)
		case <-t.done:
			t.ticker.Stop()
			return
		}
	}
}

func (t *TemplateSpawnCounter) flushCounters(ctx context.Context, dbClient *db.DB) {
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
		err := dbClient.UpdateEnvLastUsed(ctx, int64(counter.count), counter.lastUpdate, templateID)
		if err != nil {
			zap.L().Error("error updating template spawn count", zap.Error(err))
		}
	}
}

func (t *TemplateSpawnCounter) Close() {
	t.done <- true
}
