package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDecodeOverviewAcceptsOtherTopLevelFieldsAndStrictCapacity(t *testing.T) {
	t.Parallel()
	json := `{
  "generated_at":"2026-08-05T02:00:00Z",
  "api":{"status":"ok"},
  "capacity":{
    "revision":"rev-42",
    "durable_sessions":0,
    "active_workcells":0,
    "parked_workcells":0,
    "booting_workcells":0,
    "draining_workcells":0,
    "warm_target":0,
    "counted_workcells":0,
    "active_limit":25,
    "queued_limit":75,
    "planned_density_per_worker":2,
    "hard_emergency_density_per_worker":3,
    "desired_worker_hosts":2,
    "worker_host_bounds":{"min":2,"max":15},
    "fixed_control_plane_nodes":6
  }
}`
	overview, err := DecodeOverview(strings.NewReader(json))
	if err != nil {
		t.Fatal(err)
	}
	if overview.Capacity.Revision != "rev-42" || !overview.GeneratedAt.Equal(time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)) {
		t.Fatalf("unexpected overview: %+v", overview)
	}

	withUnknown := strings.Replace(json, `"fixed_control_plane_nodes":6`, `"fixed_control_plane_nodes":6,"new_ambiguous_field":1`, 1)
	if _, err := DecodeOverview(strings.NewReader(withUnknown)); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("expected strict capacity rejection, got %v", err)
	}
	missingZeroField := strings.Replace(json, `    "queued_limit":75,`+"\n", "", 1)
	if _, err := DecodeOverview(strings.NewReader(missingZeroField)); err == nil || !strings.Contains(err.Error(), "queued_limit") {
		t.Fatalf("expected required-field rejection, got %v", err)
	}
}

func TestDecodeOverviewBoundsBody(t *testing.T) {
	t.Parallel()
	tooLarge := strings.Repeat("x", 1<<20+1)
	if _, err := DecodeOverview(strings.NewReader(tooLarge)); err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("expected body-size rejection, got %v", err)
	}
	if _, err := DecodeOverview(strings.NewReader(fmt.Sprintf(`{"generated_at":"%s"}`, time.Now().Format(time.RFC3339)))); err == nil {
		t.Fatal("expected missing capacity rejection")
	}
}
