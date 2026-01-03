package orchestrator

import (
	"testing"
	"time"
)

func TestGetMaxTTLNormal(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ttl := getMaxAllowedTTL(now, now, 2*time.Hour, 3*time.Hour)
	if ttl != 2*time.Hour {
		t.Fatalf("expected 2 hours, got %v", ttl)
	}
}

func TestGetMaxTTLMax(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ttl := getMaxAllowedTTL(now, now, 4*time.Hour, 3*time.Hour)
	if ttl != 3*time.Hour {
		t.Fatalf("expected 3 hours, got %v", ttl)
	}
}

func TestGetMaxTTLExpired(t *testing.T) {
	t.Parallel()
	now := time.Now()
	ttl := getMaxAllowedTTL(now, now.Add(-2*time.Hour), 4*time.Hour, time.Hour)
	if ttl != 0 {
		t.Fatalf("expected 0 hours, got %v", ttl)
	}
}
