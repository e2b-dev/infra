package instance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

func TestAdd(t *testing.T) {
	beginningOfHour := time.Now()
	testUUID := uuid.MustParse("00000000-0000-0000-0000-000000000000")

	sandboxID := "sandboxID"
	maxInstanceLength := time.Minute

	instanceInfo := InstanceInfo{
		Instance:          &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"},
		StartTime:         beginningOfHour,
		EndTime:           beginningOfHour.Add(50 * time.Hour),
		TeamID:            &testUUID,
		MaxInstanceLength: maxInstanceLength,
	}
	cache := NewCache(nil, nil, nil, nil, nil)
	err := cache.Add(instanceInfo)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test if the instance was added to the cache
	info, err := cache.Get(sandboxID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.ExpiresAt().Sub(beginningOfHour).Round(time.Second) > maxInstanceLength {
		t.Fatalf("expected %v, got %v", maxInstanceLength, info.ExpiresAt().Sub(beginningOfHour))
	}

	if info.ExpiresAt().Sub(info.Value().EndTime).Round(time.Second) > 0 {
		t.Fatalf("expected expire and end time to be equal, got %v and %v", info.ExpiresAt(), info.Value().EndTime)
	}
}
