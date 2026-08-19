package controller

import (
	"context"
	"log/slog"
	"time"
)

type Mutator interface {
	Apply(ctx context.Context, decision Decision) error
}

type Runner struct {
	Overview   OverviewSource
	Fleet      FleetSource
	Leadership Leadership
	Engine     *Engine
	Metrics    *Metrics
	Logger     *slog.Logger
	Interval   time.Duration
	// Mutator actuates accepted scale-out decisions; nil in shadow mode. It
	// only runs on the leader path after a decision was accepted and recorded.
	Mutator Mutator
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	for {
		r.reconcile(ctx)
		select {
		case <-ctx.Done():
			closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			err := r.Leadership.Close(closeCtx)
			cancel()

			return err
		case <-ticker.C:
		}
	}
}

func (r *Runner) reconcile(ctx context.Context) {
	cycleCtx, cancel := context.WithTimeout(ctx, min(20*time.Second, r.Interval))
	defer cancel()
	leader, err := r.Leadership.Observe(cycleCtx)
	if err != nil {
		r.Engine.Invalidate()
		r.Metrics.SetLeader(false)
		r.Metrics.Fail()
		r.Logger.Error("leadership observation failed; holding", "error", err)

		return
	}
	r.Metrics.SetLeader(leader)
	if !leader {
		r.Engine.Invalidate()
		r.Metrics.Clear()
		r.Logger.Info("follower allocation; no capacity observation")

		return
	}

	overview, err := r.Overview.Fetch(cycleCtx)
	if err != nil {
		r.Engine.Invalidate()
		r.Metrics.Fail()
		r.Logger.Error("TAMS capacity observation failed; holding", "error", err)

		return
	}
	fleet, err := r.Fleet.Fetch(cycleCtx)
	if err != nil {
		r.Engine.Invalidate()
		r.Metrics.Fail()
		r.Logger.Error("Nomad fleet observation failed; holding", "error", err)

		return
	}
	decision, err := r.Engine.Evaluate(time.Now(), overview, fleet)
	if err != nil {
		r.Metrics.Fail()
		r.Logger.Error("capacity snapshot rejected; holding", "error", err, "revision", overview.Capacity.Revision, "observed_at", overview.GeneratedAt)

		return
	}
	r.Metrics.Record(decision)
	r.Logger.Info("capacity decision",
		"mode", decision.Mode,
		"revision", decision.Revision,
		"observed_at", decision.ObservedAt,
		"snapshot_age_seconds", decision.SnapshotAgeSeconds,
		"active", decision.ActiveWorkcells,
		"booting", decision.BootingWorkcells,
		"draining", decision.DrainingWorkcells,
		"parked", decision.ParkedWorkcells,
		"warm_target", decision.WarmTarget,
		"required", decision.RequiredWorkcells,
		"desired_hosts", decision.DesiredWorkerHosts,
		"actual_hosts", decision.ActualWorkerHosts,
		"draining_hosts", decision.DrainingWorkerHosts,
		"fixed_control_plane_nodes", decision.FixedControlPlaneNodes,
		"direction", decision.Direction,
		"scale_in_window_elapsed", decision.ScaleInWindowElapsed,
		"mutation_allowed", decision.MutationAllowed,
		"reason", decision.Reason,
	)
	if r.Mutator != nil {
		// A mutation failure holds actuation only; the accepted observation
		// stays valid and the next cycle retries from fresh MIG state.
		if err := r.Mutator.Apply(cycleCtx, decision); err != nil {
			r.Logger.Error("scale-out mutation failed; holding", "error", err, "revision", decision.Revision)
		}
	}
}
