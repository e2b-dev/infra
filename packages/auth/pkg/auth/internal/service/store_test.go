package service

import (
	"testing"
	"time"
)

func TestShouldWriteLastUsed(t *testing.T) {
	t.Parallel()

	base := time.Now()

	if !shouldWriteLastUsed("key-a", base) {
		t.Fatal("first write for a key must pass")
	}
	if shouldWriteLastUsed("key-a", base.Add(lastUsedWriteWindow/2)) {
		t.Fatal("write inside the window must be suppressed")
	}
	if !shouldWriteLastUsed("key-b", base) {
		t.Fatal("independent key must not be suppressed")
	}
	if !shouldWriteLastUsed("key-a", base.Add(lastUsedWriteWindow+time.Second)) {
		t.Fatal("write after the window must pass")
	}
}
