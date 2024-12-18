package utils

import (
	"context"
	"sync"
	"time"

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

func NewTemplateSpawnCounter(tickerDuration time.Duration, dbClient *db.DB) *TemplateSpawnCounter {
	counter := &TemplateSpawnCounter{
		counters: make(map[string]*TemplateCounter),
		ticker:   time.NewTicker(tickerDuration),
		done:     make(chan bool),
	}

	go counter.processUpdates(dbClient, tickerDuration)
	return counter
}

func (t *TemplateSpawnCounter) IncreaseTemplateSpawnCount(templateID string) {
	t.mu.Lock()
	if _, exists := t.counters[templateID]; !exists {
		t.counters[templateID] = &TemplateCounter{}
	}
	t.counters[templateID].count++
	t.counters[templateID].lastUpdate = time.Now()
	t.mu.Unlock()
}

func (t *TemplateSpawnCounter) processUpdates(dbClient *db.DB, tickerDuration time.Duration) {
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
		dbClient.UpdateEnvLastUsed(context.Background(), int64(counter.count), counter.lastUpdate, templateID)
	}
}

func (t *TemplateSpawnCounter) Stop() {
	t.done <- true
}
