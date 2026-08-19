package controller

import (
	"errors"
	"fmt"
	"time"
)

type Engine struct {
	// ScaleOutMutationEnabled marks scale-out decisions as actuatable. It can
	// never mark scale-in: that stays decision-only until a typed drain owner
	// exists on both sides of the capacity contract.
	ScaleOutMutationEnabled bool

	lastRevision    string
	lastDigest      [32]byte
	lastObservedAt  time.Time
	lastEvaluatedAt time.Time
	lowDemandSince  *time.Time
	lowDemandTarget int
}

func (e *Engine) Evaluate(now time.Time, overview Overview, fleet Fleet) (Decision, error) {
	if err := validateSnapshot(now, overview, fleet); err != nil {
		e.Invalidate()

		return Decision{}, err
	}
	if !e.lastEvaluatedAt.IsZero() {
		gap := now.Sub(e.lastEvaluatedAt)
		if gap < 0 {
			e.Invalidate()

			return Decision{}, errors.New("controller evaluation time regressed")
		}
		if gap > MaximumObservationGap {
			e.Invalidate()
		}
	}

	digest, err := capacityDigest(overview.Capacity)
	if err != nil {
		e.Invalidate()

		return Decision{}, err
	}
	if !e.lastObservedAt.IsZero() {
		if overview.GeneratedAt.Before(e.lastObservedAt) {
			e.Invalidate()

			return Decision{}, fmt.Errorf("capacity observed_at regressed from %s to %s", e.lastObservedAt.Format(time.RFC3339Nano), overview.GeneratedAt.Format(time.RFC3339Nano))
		}
		if overview.Capacity.Revision == e.lastRevision && digest != e.lastDigest {
			e.Invalidate()

			return Decision{}, fmt.Errorf("capacity revision %q was reused for different data", overview.Capacity.Revision)
		}
		if overview.Capacity.Revision != e.lastRevision && !overview.GeneratedAt.After(e.lastObservedAt) {
			e.Invalidate()

			return Decision{}, errors.New("new capacity revision requires a later observed_at")
		}
	}

	e.lastRevision = overview.Capacity.Revision
	e.lastDigest = digest
	e.lastObservedAt = overview.GeneratedAt
	e.lastEvaluatedAt = now

	required := RequiredWorkcells(overview.Capacity)
	desired := DesiredHosts(required)
	mode := "shadow"
	if e.ScaleOutMutationEnabled {
		mode = "scale-out"
	}
	decision := Decision{
		Mode:                   mode,
		Revision:               overview.Capacity.Revision,
		ObservedAt:             overview.GeneratedAt,
		SnapshotAgeSeconds:     max(0, now.Sub(overview.GeneratedAt).Seconds()),
		DurableSessions:        overview.Capacity.DurableSessions,
		ActiveWorkcells:        overview.Capacity.ActiveWorkcells,
		BootingWorkcells:       overview.Capacity.BootingWorkcells,
		DrainingWorkcells:      overview.Capacity.DrainingWorkcells,
		ParkedWorkcells:        overview.Capacity.ParkedWorkcells,
		WarmTarget:             *overview.Capacity.WarmTarget,
		RequiredWorkcells:      required,
		DesiredWorkerHosts:     desired,
		ActualWorkerHosts:      fleet.ActualHosts,
		DrainingWorkerHosts:    fleet.DrainingHosts,
		FixedControlPlaneNodes: overview.Capacity.FixedControlPlaneNodes,
		MutationAllowed:        false,
	}

	switch {
	case desired > fleet.ActualHosts:
		e.lowDemandSince = nil
		e.lowDemandTarget = 0
		decision.Direction = DirectionScaleOut
		decision.MutationAllowed = e.ScaleOutMutationEnabled
		decision.Reason = "desired hosts exceed observed Nomad worker hosts"
	case desired < fleet.ActualHosts:
		if e.lowDemandSince == nil || e.lowDemandTarget != desired {
			started := now
			e.lowDemandSince = &started
			e.lowDemandTarget = desired
		}
		elapsed := now.Sub(*e.lowDemandSince)
		decision.Direction = DirectionScaleIn
		decision.LowDemandSince = e.lowDemandSince
		decision.LowDemandSeconds = max(0, elapsed.Seconds())
		decision.ScaleInWindowElapsed = elapsed >= ScaleInWindow
		decision.RequiresDrainVerification = true
		if decision.ScaleInWindowElapsed {
			decision.Reason = "low-demand window elapsed; a future mutating controller must still drain and prove zero allocations/workcells"
		} else {
			decision.Reason = "waiting for continuous 15-minute low-demand window"
		}
	default:
		e.lowDemandSince = nil
		e.lowDemandTarget = 0
		decision.Direction = DirectionHold
		decision.Reason = "desired and observed worker hosts match"
	}

	return decision, nil
}

// Invalidate breaks scale-in continuity. A later valid sample must establish a
// new complete low-demand window; stale or ambiguous data can never advance it.
func (e *Engine) Invalidate() {
	e.lowDemandSince = nil
	e.lowDemandTarget = 0
}

func validateSnapshot(now time.Time, overview Overview, fleet Fleet) error {
	capacity := overview.Capacity
	if overview.GeneratedAt.IsZero() {
		return errors.New("capacity generated_at is required")
	}
	if age := now.Sub(overview.GeneratedAt); age > MaximumSnapshotAge {
		return fmt.Errorf("capacity snapshot is stale by %s", age.Round(time.Second))
	} else if age < -MaximumFutureSkew {
		return fmt.Errorf("capacity snapshot is %s in the future", (-age).Round(time.Second))
	}
	if !revisionPattern.MatchString(capacity.Revision) {
		return errors.New("capacity revision is missing or invalid")
	}
	if capacity.WarmTarget == nil {
		return errors.New("capacity warm_target is missing")
	}

	counts := map[string]int{
		"durable_sessions":          capacity.DurableSessions,
		"active_workcells":          capacity.ActiveWorkcells,
		"parked_workcells":          capacity.ParkedWorkcells,
		"booting_workcells":         capacity.BootingWorkcells,
		"draining_workcells":        capacity.DrainingWorkcells,
		"warm_target":               *capacity.WarmTarget,
		"counted_workcells":         capacity.CountedWorkcells,
		"active_limit":              capacity.ActiveLimit,
		"queued_limit":              capacity.QueuedLimit,
		"fixed_control_plane_nodes": capacity.FixedControlPlaneNodes,
		"actual_worker_hosts":       fleet.ActualHosts,
		"draining_worker_hosts":     fleet.DrainingHosts,
	}
	for name, count := range counts {
		if count < 0 {
			return fmt.Errorf("capacity %s cannot be negative", name)
		}
		if count > MaximumReportedCount {
			return fmt.Errorf("capacity %s exceeds the bounded observation limit", name)
		}
	}
	// durable_sessions counts lifetime session rows, so it grows without
	// bound; live dev exceeded 100 within weeks and wedged the observer in a
	// permanent hold. Only the bounded-observation limit applies to it — the
	// invited-beta envelope is enforced on active/queued/density/bounds.
	if capacity.ActiveLimit != InvitedBetaActiveLimit {
		return fmt.Errorf("capacity active_limit must remain %d for invited beta", InvitedBetaActiveLimit)
	}
	if capacity.QueuedLimit != InvitedBetaQueuedLimit {
		return fmt.Errorf("capacity queued_limit must remain %d for invited beta", InvitedBetaQueuedLimit)
	}
	if capacity.ActiveWorkcells > capacity.ActiveLimit {
		return errors.New("capacity active_workcells exceeds active_limit")
	}
	if capacity.ActiveWorkcells+capacity.DrainingWorkcells > capacity.DurableSessions {
		return errors.New("active and draining session workcells exceed durable_sessions")
	}
	if capacity.ActiveWorkcells+capacity.DrainingWorkcells > InvitedBetaActiveLimit {
		return fmt.Errorf("active and draining session workcells exceed the invited-beta limit of %d", InvitedBetaActiveLimit)
	}
	if capacity.PlannedDensityPerWorker != PlannedDensity || capacity.HardEmergencyDensityPerWorker != EmergencyDensity {
		return fmt.Errorf("capacity density contract must remain planned=%d emergency=%d", PlannedDensity, EmergencyDensity)
	}
	if capacity.WorkerHostBounds != (HostBounds{Min: MinimumWorkerHosts, Max: MaximumWorkerHosts}) {
		return fmt.Errorf("capacity worker_host_bounds must remain %d..%d", MinimumWorkerHosts, MaximumWorkerHosts)
	}
	if capacity.FixedControlPlaneNodes != FixedControlPlaneNodes {
		return fmt.Errorf("capacity fixed_control_plane_nodes must remain %d", FixedControlPlaneNodes)
	}
	required := RequiredWorkcells(capacity)
	if required > EmergencyFleetCapacity {
		return fmt.Errorf("capacity requires %d workcells, above the %d-workcell hard emergency fleet ceiling", required, EmergencyFleetCapacity)
	}
	if capacity.CountedWorkcells != required {
		return fmt.Errorf("capacity counted_workcells=%d contradicts recomputed required=%d", capacity.CountedWorkcells, required)
	}
	desired := DesiredHosts(required)
	if capacity.DesiredWorkerHosts != desired {
		return fmt.Errorf("capacity desired_worker_hosts=%d contradicts recomputed desired=%d", capacity.DesiredWorkerHosts, desired)
	}
	if fleet.DrainingHosts > fleet.ActualHosts {
		return errors.New("nomad draining hosts exceed actual hosts")
	}
	if fleet.ActualHosts > MaximumWorkerHosts+1 {
		return fmt.Errorf("nomad reports %d worker hosts, above the %d-host bound plus one replacement", fleet.ActualHosts, MaximumWorkerHosts)
	}

	return nil
}
