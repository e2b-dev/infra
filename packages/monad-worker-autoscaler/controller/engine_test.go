package controller

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDesiredHostsFormula(t *testing.T) {
	t.Parallel()
	tests := []struct {
		required int
		desired  int
	}{
		{0, 2},
		{1, 2},
		{2, 2},
		{3, 3},
		{4, 3},
		{25, 14},
		{28, 15},
		{100, 15},
	}
	for _, test := range tests {
		if got := DesiredHosts(test.required); got != test.desired {
			t.Errorf("DesiredHosts(%d) = %d, want %d", test.required, got, test.desired)
		}
	}
}

func TestEvaluateUsesWarmMaximum(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	overview := validOverview(now)
	overview.Capacity.ActiveWorkcells = 3
	overview.Capacity.DurableSessions = 3
	overview.Capacity.ParkedWorkcells = 1
	warm := 4
	overview.Capacity.WarmTarget = &warm
	overview.Capacity.CountedWorkcells = 7
	overview.Capacity.DesiredWorkerHosts = 5

	decision, err := (&Engine{}).Evaluate(now, overview, Fleet{ActualHosts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if decision.RequiredWorkcells != 7 || decision.DesiredWorkerHosts != 5 || decision.Direction != DirectionScaleOut {
		t.Fatalf("unexpected decision: %+v", decision)
	}
	if decision.MutationAllowed {
		t.Fatal("shadow decision must never allow mutation")
	}
}

func TestWarmPoolBootingDoesNotRequireDurableSession(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	overview := validOverview(now)
	overview.Capacity.BootingWorkcells = 2
	warm := 2
	overview.Capacity.WarmTarget = &warm
	overview.Capacity.CountedWorkcells = 4
	overview.Capacity.DesiredWorkerHosts = 3
	decision, err := (&Engine{}).Evaluate(now, overview, Fleet{ActualHosts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if decision.DurableSessions != 0 || decision.RequiredWorkcells != 4 || decision.DesiredWorkerHosts != 3 {
		t.Fatalf("warm-only capacity was not computed by the exact formula: %+v", decision)
	}
}

func TestInvitedBetaEnvelopeAndEmergencyBoundary(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		mutate   func(*Capacity)
		required int
	}{
		{
			name: "normal invited-beta maximum",
			mutate: func(capacity *Capacity) {
				capacity.DurableSessions = MaximumDurableSessions
				capacity.ActiveWorkcells = InvitedBetaActiveLimit
				capacity.ParkedWorkcells = 2
				warm := 2
				capacity.WarmTarget = &warm
				capacity.CountedWorkcells = 27
				capacity.DesiredWorkerHosts = MaximumWorkerHosts
			},
			required: 27,
		},
		{
			name: "hard emergency ceiling",
			mutate: func(capacity *Capacity) {
				capacity.DurableSessions = MaximumDurableSessions
				capacity.ActiveWorkcells = InvitedBetaActiveLimit
				capacity.BootingWorkcells = EmergencyFleetCapacity - InvitedBetaActiveLimit
				capacity.CountedWorkcells = EmergencyFleetCapacity
				capacity.DesiredWorkerHosts = MaximumWorkerHosts
			},
			required: EmergencyFleetCapacity,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			overview := validOverview(now)
			test.mutate(&overview.Capacity)
			decision, err := (&Engine{}).Evaluate(now, overview, Fleet{ActualHosts: MaximumWorkerHosts})
			if err != nil {
				t.Fatal(err)
			}
			if decision.RequiredWorkcells != test.required || decision.DesiredWorkerHosts != MaximumWorkerHosts || decision.MutationAllowed {
				t.Fatalf("unexpected boundary decision: %+v", decision)
			}
		})
	}
}

func TestScaleInRequiresContinuousWindow(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	engine := &Engine{}
	overview := validOverview(now)
	overview.Capacity.Revision = "r1"

	first, err := engine.Evaluate(now, overview, Fleet{ActualHosts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if first.ScaleInWindowElapsed || first.LowDemandSince == nil {
		t.Fatalf("first low-demand sample should start but not satisfy window: %+v", first)
	}

	var second Decision
	for minute := 1; minute <= int(ScaleInWindow/time.Minute); minute++ {
		secondAt := now.Add(time.Duration(minute) * time.Minute)
		overview.GeneratedAt = secondAt
		overview.Capacity.Revision = fmt.Sprintf("r%d", minute+1)
		second, err = engine.Evaluate(secondAt, overview, Fleet{ActualHosts: 3})
		if err != nil {
			t.Fatal(err)
		}
	}
	if !second.ScaleInWindowElapsed || !second.RequiresDrainVerification || second.MutationAllowed {
		t.Fatalf("elapsed window must remain a non-mutating recommendation: %+v", second)
	}

	engine.Invalidate()
	thirdAt := now.Add(ScaleInWindow + time.Second)
	overview.GeneratedAt = thirdAt
	overview.Capacity.Revision = "r3"
	third, err := engine.Evaluate(thirdAt, overview, Fleet{ActualHosts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if third.ScaleInWindowElapsed || third.LowDemandSeconds != 0 {
		t.Fatalf("invalid data must reset scale-in continuity: %+v", third)
	}
}

func TestScaleInWindowRequiresBoundedObservationGaps(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	engine := &Engine{}
	overview := validOverview(now)
	if _, err := engine.Evaluate(now, overview, Fleet{ActualHosts: 3}); err != nil {
		t.Fatal(err)
	}

	afterGap := now.Add(ScaleInWindow)
	overview.GeneratedAt = afterGap
	overview.Capacity.Revision = "r2"
	decision, err := engine.Evaluate(afterGap, overview, Fleet{ActualHosts: 3})
	if err != nil {
		t.Fatal(err)
	}
	if decision.LowDemandSeconds != 0 || decision.ScaleInWindowElapsed {
		t.Fatalf("an observation gap cannot prove continuous low demand: %+v", decision)
	}
}

func TestScaleInWindowResetsWhenDesiredTargetChanges(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	engine := &Engine{}
	overview := validOverview(now)
	if _, err := engine.Evaluate(now, overview, Fleet{ActualHosts: 4}); err != nil {
		t.Fatal(err)
	}

	changedAt := now.Add(time.Minute)
	overview.GeneratedAt = changedAt
	overview.Capacity.Revision = "r2"
	overview.Capacity.DurableSessions = 3
	overview.Capacity.ActiveWorkcells = 3
	overview.Capacity.CountedWorkcells = 3
	overview.Capacity.DesiredWorkerHosts = 3
	decision, err := engine.Evaluate(changedAt, overview, Fleet{ActualHosts: 4})
	if err != nil {
		t.Fatal(err)
	}
	if decision.LowDemandSeconds != 0 || decision.ScaleInWindowElapsed {
		t.Fatalf("changed target must establish a new scale-in window: %+v", decision)
	}
}

func TestEvaluateFailsClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*Overview, *Fleet)
		want   string
	}{
		{"missing revision", func(o *Overview, _ *Fleet) { o.Capacity.Revision = "" }, "revision"},
		{"missing warm target", func(o *Overview, _ *Fleet) { o.Capacity.WarmTarget = nil }, "warm_target"},
		{"stale", func(o *Overview, _ *Fleet) { o.GeneratedAt = now.Add(-MaximumSnapshotAge - time.Second) }, "stale"},
		{"future", func(o *Overview, _ *Fleet) { o.GeneratedAt = now.Add(MaximumFutureSkew + time.Second) }, "future"},
		{"negative", func(o *Overview, _ *Fleet) { o.Capacity.ActiveWorkcells = -1 }, "negative"},
		{"unbounded", func(o *Overview, _ *Fleet) { o.Capacity.DurableSessions = MaximumReportedCount + 1 }, "bounded"},
		{"durable beta cap", func(o *Overview, _ *Fleet) { o.Capacity.DurableSessions = MaximumDurableSessions + 1 }, "invited-beta limit"},
		{"active limit drift", func(o *Overview, _ *Fleet) { o.Capacity.ActiveLimit = InvitedBetaActiveLimit + 1 }, "active_limit"},
		{"queued limit drift", func(o *Overview, _ *Fleet) { o.Capacity.QueuedLimit = InvitedBetaQueuedLimit + 1 }, "queued_limit"},
		{"session contradiction", func(o *Overview, _ *Fleet) { o.Capacity.DurableSessions = 0; o.Capacity.ActiveWorkcells = 1 }, "durable_sessions"},
		{"session workcell beta cap", func(o *Overview, _ *Fleet) {
			o.Capacity.DurableSessions = InvitedBetaActiveLimit + 1
			o.Capacity.ActiveWorkcells = InvitedBetaActiveLimit
			o.Capacity.DrainingWorkcells = 1
			o.Capacity.CountedWorkcells = InvitedBetaActiveLimit + 1
			o.Capacity.DesiredWorkerHosts = MaximumWorkerHosts
		}, "invited-beta limit"},
		{"emergency fleet cap", func(o *Overview, _ *Fleet) {
			o.Capacity.DurableSessions = InvitedBetaActiveLimit
			o.Capacity.ActiveWorkcells = InvitedBetaActiveLimit
			o.Capacity.BootingWorkcells = EmergencyFleetCapacity - InvitedBetaActiveLimit + 1
			o.Capacity.CountedWorkcells = EmergencyFleetCapacity + 1
			o.Capacity.DesiredWorkerHosts = MaximumWorkerHosts
		}, "hard emergency"},
		{"density drift", func(o *Overview, _ *Fleet) { o.Capacity.PlannedDensityPerWorker = 3 }, "density"},
		{"fixed-node drift", func(o *Overview, _ *Fleet) { o.Capacity.FixedControlPlaneNodes = 5 }, "fixed_control_plane_nodes"},
		{"count contradiction", func(o *Overview, _ *Fleet) { o.Capacity.CountedWorkcells++ }, "counted_workcells"},
		{"desired contradiction", func(o *Overview, _ *Fleet) { o.Capacity.DesiredWorkerHosts++ }, "desired_worker_hosts"},
		{"ambiguous Nomad", func(_ *Overview, f *Fleet) { f.ActualHosts = 17 }, "above"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			overview := validOverview(now)
			fleet := Fleet{ActualHosts: 2}
			test.mutate(&overview, &fleet)
			_, err := (&Engine{}).Evaluate(now, overview, fleet)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestRevisionReuseAndObservedRegressionFailClosed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 5, 2, 0, 0, 0, time.UTC)
	engine := &Engine{}
	overview := validOverview(now)
	if _, err := engine.Evaluate(now, overview, Fleet{ActualHosts: 2}); err != nil {
		t.Fatal(err)
	}

	reused := overview
	reused.GeneratedAt = now.Add(time.Second)
	reused.Capacity.DurableSessions++
	if _, err := engine.Evaluate(now.Add(time.Second), reused, Fleet{ActualHosts: 2}); err == nil || !strings.Contains(err.Error(), "reused") {
		t.Fatalf("expected reused revision rejection, got %v", err)
	}

	engine = &Engine{}
	if _, err := engine.Evaluate(now, overview, Fleet{ActualHosts: 2}); err != nil {
		t.Fatal(err)
	}
	regressed := overview
	regressed.Capacity.Revision = "r2"
	regressed.GeneratedAt = now.Add(-time.Second)
	if _, err := engine.Evaluate(now, regressed, Fleet{ActualHosts: 2}); err == nil || !strings.Contains(err.Error(), "regressed") {
		t.Fatalf("expected observed_at regression rejection, got %v", err)
	}
}

func validOverview(observed time.Time) Overview {
	warm := 0

	return Overview{
		GeneratedAt: observed,
		Capacity: Capacity{
			Revision:                      "r1",
			DurableSessions:               0,
			ActiveWorkcells:               0,
			ParkedWorkcells:               0,
			BootingWorkcells:              0,
			DrainingWorkcells:             0,
			WarmTarget:                    &warm,
			CountedWorkcells:              0,
			ActiveLimit:                   InvitedBetaActiveLimit,
			QueuedLimit:                   InvitedBetaQueuedLimit,
			PlannedDensityPerWorker:       PlannedDensity,
			HardEmergencyDensityPerWorker: EmergencyDensity,
			DesiredWorkerHosts:            MinimumWorkerHosts,
			WorkerHostBounds:              HostBounds{Min: MinimumWorkerHosts, Max: MaximumWorkerHosts},
			FixedControlPlaneNodes:        FixedControlPlaneNodes,
		},
	}
}

func TestScaleOutMutationEnabledGatesMutationAllowed(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 8, 19, 2, 0, 0, 0, time.UTC)

	scaleOut := validOverview(now)
	scaleOut.Capacity.ActiveWorkcells = 6
	scaleOut.Capacity.DurableSessions = 6
	scaleOut.Capacity.CountedWorkcells = 6
	scaleOut.Capacity.DesiredWorkerHosts = 4

	engine := &Engine{ScaleOutMutationEnabled: true}
	decision, err := engine.Evaluate(now, scaleOut, Fleet{ActualHosts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != DirectionScaleOut || !decision.MutationAllowed {
		t.Fatalf("expected mutation-allowed scale-out, got %+v", decision)
	}
	if decision.Mode != "scale-out" {
		t.Fatalf("expected scale-out mode, got %q", decision.Mode)
	}

	hold := validOverview(now.Add(time.Second))
	hold.Capacity.ActiveWorkcells = 6
	hold.Capacity.DurableSessions = 6
	hold.Capacity.CountedWorkcells = 6
	hold.Capacity.DesiredWorkerHosts = 4
	hold.Capacity.Revision = "revision-hold-1"
	decision, err = engine.Evaluate(now.Add(time.Second), hold, Fleet{ActualHosts: 4})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != DirectionHold || decision.MutationAllowed {
		t.Fatalf("hold must never allow mutation: %+v", decision)
	}

	scaleIn := validOverview(now.Add(2 * time.Second))
	scaleIn.Capacity.Revision = "revision-scale-in-1"
	decision, err = engine.Evaluate(now.Add(2*time.Second), scaleIn, Fleet{ActualHosts: 5})
	if err != nil {
		t.Fatal(err)
	}
	if decision.Direction != DirectionScaleIn || decision.MutationAllowed || !decision.RequiresDrainVerification {
		t.Fatalf("scale-in must never allow mutation: %+v", decision)
	}

	shadow := &Engine{}
	decision, err = shadow.Evaluate(now, scaleOut, Fleet{ActualHosts: 2})
	if err != nil {
		t.Fatal(err)
	}
	if decision.MutationAllowed || decision.Mode != "shadow" {
		t.Fatalf("shadow engine must not allow mutation: %+v", decision)
	}
}
