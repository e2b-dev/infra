package utils

import (
	"context"
	"sync"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/db"
)

type TemplateSpawnCounter struct {
	counters map[string]int
	mu       sync.Mutex
	ticker   *time.Ticker
	done     chan bool
}

func NewTemplateSpawnCounter(tickerDuration time.Duration, dbClient *db.DB) *TemplateSpawnCounter {
	counter := &TemplateSpawnCounter{
		counters: make(map[string]int),
		ticker:   time.NewTicker(tickerDuration),
		done:     make(chan bool),
	}

	go counter.processUpdates(dbClient)
	return counter
}

func (t *TemplateSpawnCounter) IncreaseTemplateSpawnCount(templateID string) {
	t.mu.Lock()
	t.counters[templateID]++
	t.mu.Unlock()
}

func (t *TemplateSpawnCounter) processUpdates(dbClient *db.DB) {
	for {
		select {
		case <-t.ticker.C:
			t.flushCounters(dbClient)
		case <-t.done:
			t.ticker.Stop()
			return
		}
	}
}

func (t *TemplateSpawnCounter) flushCounters(dbClient *db.DB) {
	t.mu.Lock()
	updates := make(map[string]int)
	for templateID, count := range t.counters {
		if count > 0 {
			updates[templateID] = count
		}
	}
	// Clear the counters
	t.counters = make(map[string]int)
	t.mu.Unlock()

	for templateID, count := range updates {
		dbClient.UpdateEnvLastUsed(context.Background(), int64(count), templateID)
	}
}

func (t *TemplateSpawnCounter) Stop() {
	t.done <- true
}
