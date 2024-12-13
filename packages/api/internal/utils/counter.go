package utils

import (
	"sync"
	"time"
)

type TemplateSpawnCounter struct {
	counters map[string]int
	mu       sync.Mutex
	ticker   *time.Ticker
	done     chan bool
}

func NewTemplateSpawnCounter(tickerDuration time.Duration, processingFunction func(templateID string, count int) error) *TemplateSpawnCounter {
	counter := &TemplateSpawnCounter{
		counters: make(map[string]int),
		ticker:   time.NewTicker(tickerDuration),
		done:     make(chan bool),
	}

	go counter.processUpdates(processingFunction)
	return counter
}

func (t *TemplateSpawnCounter) IncreaseTemplateSpawnCount(templateID string) {
	t.mu.Lock()
	t.counters[templateID]++
	t.mu.Unlock()
}

func (t *TemplateSpawnCounter) processUpdates(processingFunction func(templateID string, count int) error) {
	for {
		select {
		case <-t.ticker.C:
			t.flushCounters(processingFunction)
		case <-t.done:
			t.ticker.Stop()
			return
		}
	}
}

func (t *TemplateSpawnCounter) flushCounters(processingFunction func(templateID string, count int) error) {
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
		processingFunction(templateID, count)
	}
}

func (t *TemplateSpawnCounter) Stop() {
	t.done <- true
}
