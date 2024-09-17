package instance

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
)

const sandboxID = "sandboxID"
const maxInstanceLength = time.Minute

var beginningOfHour = time.Now()
var teamID = uuid.MustParse("00000000-0000-0000-0000-000000000000")

func TestAdd(t *testing.T) {
	instanceInfo := InstanceInfo{
		Instance:          &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"},
		StartTime:         beginningOfHour,
		EndTime:           beginningOfHour.Add(50 * time.Hour),
		TeamID:            &teamID,
		MaxInstanceLength: maxInstanceLength,
	}

	cache := NewCache(nil, nil, nil, nil, nil)
	err := cache.Add(instanceInfo, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test the instance was added to the cache
	exists := cache.Exists(sandboxID)
	if !exists {
		t.Fatalf("expected instance to exist in the cache")
	}

	// Get the instance from the cache by ID
	info, err := cache.Get(sandboxID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Test the max instance length is respected
	if info.ExpiresAt().Sub(beginningOfHour).Round(time.Second) > maxInstanceLength {
		t.Fatalf("expected %v, got %v", maxInstanceLength, info.ExpiresAt().Sub(beginningOfHour))
	}

	// Test the end time is equal to the expire time
	if info.ExpiresAt().Sub(info.Value().EndTime).Round(time.Second) > 0 {
		t.Fatalf("expected expire and end time to be equal, got %v and %v", info.ExpiresAt(), info.Value().EndTime)
	}
}

func TestTimeInPastForNewSandbox(t *testing.T) {
	cache := NewCache(nil, nil, nil, nil, nil)

	// times in past
	endTimeInPast := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"}, StartTime: beginningOfHour.Add(-2 * time.Hour), EndTime: beginningOfHour.Add(-time.Hour), TeamID: &teamID, MaxInstanceLength: 4 * time.Hour}
	err := cache.Add(endTimeInPast, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	instance, err := cache.Get(sandboxID)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if instance.ExpiresAt().Sub(time.Now()).Round(time.Second) != time.Hour {
		t.Fatalf("expected to respect the timeout and expire in 1 hour, got %v", instance.ExpiresAt().Sub(time.Now()))
	}
}

func TestInvalidTimes(t *testing.T) {
	cache := NewCache(nil, nil, nil, nil, nil)

	// end time before start time
	endBeforeStart := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"}, StartTime: beginningOfHour, EndTime: beginningOfHour.Add(-time.Second), TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err := cache.Add(endBeforeStart, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	if cache.cache.Len() != 0 {
		t.Fatalf("expected cache to be empty, got %d", cache.cache.Len())
	}

	// times in past
	endTimeInPast := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"}, StartTime: beginningOfHour.Add(-2 * time.Hour), EndTime: beginningOfHour.Add(-time.Hour), TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err = cache.Add(endTimeInPast, false)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	if cache.cache.Len() != 0 {
		t.Fatalf("expected cache to be empty, got %d", cache.cache.Len())
	}
}

func TestInvalidEntries(t *testing.T) {
	cache := NewCache(nil, nil, nil, nil, nil)

	// No instance
	noInstance := InstanceInfo{StartTime: beginningOfHour, EndTime: beginningOfHour.Add(50 * time.Hour), TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err := cache.Add(noInstance, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	if cache.cache.Len() != 0 {
		t.Fatalf("expected cache to be empty, got %d", cache.cache.Len())
	}

	// No team ID
	noTeamID := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"}, StartTime: beginningOfHour, EndTime: beginningOfHour.Add(50 * time.Hour), MaxInstanceLength: maxInstanceLength}
	err = cache.Add(noTeamID, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	if cache.cache.Len() != 0 {
		t.Fatalf("expected cache to be empty, got %d", cache.cache.Len())
	}

	// No sandbox ID
	noSandboxID := InstanceInfo{Instance: &api.Sandbox{TemplateID: "Unknown", ClientID: "Unknown"}, StartTime: beginningOfHour, EndTime: beginningOfHour.Add(50 * time.Hour), TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err = cache.Add(noSandboxID, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	if cache.cache.Len() != 0 {
		t.Fatalf("expected cache to be empty, got %d", cache.cache.Len())
	}

	// No client ID
	noClientID := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown"}, StartTime: beginningOfHour, EndTime: beginningOfHour.Add(50 * time.Hour), TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err = cache.Add(noClientID, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	// No end time
	noEndTime := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"}, StartTime: beginningOfHour, TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err = cache.Add(noEndTime, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}

	// No start time
	noStartTime := InstanceInfo{Instance: &api.Sandbox{SandboxID: sandboxID, TemplateID: "Unknown", ClientID: "Unknown"}, EndTime: beginningOfHour.Add(50 * time.Hour), TeamID: &teamID, MaxInstanceLength: maxInstanceLength}
	err = cache.Add(noStartTime, true)
	if err == nil {
		t.Fatalf("expected error, but got nil")
	}
}
