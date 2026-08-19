package controller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
)

// ScaleOutMutator is the only component allowed to touch the worker group,
// and it can only grow it. The floor is the Terraform-reviewed cluster size:
// resizes never go below it, and shrinking is refused entirely because no
// typed drain owner exists yet on either side of the capacity contract.
type ScaleOutMutator struct {
	actuator Actuator
	floor    int
	metrics  *Metrics
	logger   *slog.Logger
}

func NewScaleOutMutator(actuator Actuator, floor int, metrics *Metrics, logger *slog.Logger) (*ScaleOutMutator, error) {
	if actuator == nil {
		return nil, errors.New("scale-out actuator is required")
	}
	if metrics == nil {
		return nil, errors.New("scale-out metrics are required")
	}
	if logger == nil {
		return nil, errors.New("scale-out logger is required")
	}
	if floor < MinimumWorkerHosts || floor > MaximumWorkerHosts {
		return nil, fmt.Errorf("worker host floor must be from %d to %d, got %d", MinimumWorkerHosts, MaximumWorkerHosts, floor)
	}

	return &ScaleOutMutator{actuator: actuator, floor: floor, metrics: metrics, logger: logger}, nil
}

func (m *ScaleOutMutator) Apply(ctx context.Context, decision Decision) error {
	if decision.Direction != DirectionScaleOut || !decision.MutationAllowed {
		return nil
	}
	target := decision.DesiredWorkerHosts
	if target < m.floor {
		target = m.floor
	}
	if target > MaximumWorkerHosts {
		m.metrics.Fail()

		return fmt.Errorf("scale-out target %d exceeds the %d-host ceiling", target, MaximumWorkerHosts)
	}
	current, err := m.actuator.TargetSize(ctx)
	if err != nil {
		m.metrics.Fail()

		return fmt.Errorf("read worker group target: %w", err)
	}
	if current > MaximumWorkerHosts+1 {
		m.metrics.Fail()

		return fmt.Errorf("observed worker group target %d is outside the reviewed envelope", current)
	}
	m.metrics.SetMIGTarget(current)
	if target <= current {
		// The group already covers the target; a resize requested on a prior
		// cycle may still be booting hosts Nomad has not admitted yet.
		return nil
	}
	if err := m.actuator.Resize(ctx, target); err != nil {
		m.metrics.Fail()

		return fmt.Errorf("resize worker group to %d: %w", target, err)
	}
	m.metrics.RecordResize(target)
	m.logger.Info(
		"worker scale-out resize applied",
		"previous_target", current,
		"new_target", target,
		"desired_worker_hosts", decision.DesiredWorkerHosts,
		"floor", m.floor,
		"revision", decision.Revision,
	)

	return nil
}
