package service

import (
	"sync"
	"testing"
	"time"
)

func TestShouldWriteLastUsed(t *testing.T) {
	t.Parallel()

	// Unique keys per invocation: the debounce map is package state shared
	// across repeated in-process runs (-count=2).
	keyA := "key-a-" + t.Name() + time.Now().String()
	keyB := "key-b-" + t.Name() + time.Now().String()
	base := time.Now()

	if !shouldWriteLastUsed(keyA, base) {
		t.Fatal("first write for a key must pass")
	}
	if shouldWriteLastUsed(keyA, base.Add(lastUsedWriteWindow/2)) {
		t.Fatal("write inside the window must be suppressed")
	}
	if !shouldWriteLastUsed(keyB, base) {
		t.Fatal("independent key must not be suppressed")
	}
	if !shouldWriteLastUsed(keyA, base.Add(lastUsedWriteWindow+time.Second)) {
		t.Fatal("write after the window must pass")
	}
}

func TestShouldWriteLastUsedConcurrent(t *testing.T) {
	t.Parallel()

	key := "key-conc-" + time.Now().String()
	base := time.Now()
	shouldWriteLastUsed(key, base)

	// After the window expires, exactly one concurrent caller wins.
	later := base.Add(lastUsedWriteWindow + time.Second)
	const n = 16
	wins := make(chan bool, n)
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			wins <- shouldWriteLastUsed(key, later)
		}()
	}
	wg.Wait()
	close(wins)
	won := 0
	for w := range wins {
		if w {
			won++
		}
	}
	if won != 1 {
		t.Fatalf("expected exactly one winner, got %d", won)
	}
}
