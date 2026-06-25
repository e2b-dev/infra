//go:build linux

package service

import (
	"context"
	"slices"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

// DrainCoordinator keeps service status publication and admission-gate closure
// ordered so Draining is never observable before new work is rejected.
type DrainCoordinator struct {
	info       *ServiceInfo
	controller DrainController
}

func NewDrainCoordinator(info *ServiceInfo, controller DrainController) *DrainCoordinator {
	return &DrainCoordinator{info: info, controller: controller}
}

func (d *DrainCoordinator) StartDraining(ctx context.Context) {
	if d == nil || d.controller == nil {
		return
	}

	d.controller.StartDraining(ctx)
}

// BeginDraining transitions the service toward Draining. It always closes the
// admission gate before publishing the Draining status, so new work is rejected
// before the transition becomes observable. validate, when non-nil, can reject
// the request based on the current status (the gate stays open if it fails). It
// is a no-op once the service is already draining (the gate is already closed,
// since that is the only way the status reaches Draining).
//
// publishFrom controls when the Draining status is published:
//   - empty: always publish (unless already draining) — the explicit-request
//     (API) path.
//   - non-empty: publish only when the current status is one of publishFrom, so
//     an operator-set status (e.g. Unhealthy) is never overwritten — the
//     shutdown path, where the gate must close regardless of status.
func (d *DrainCoordinator) BeginDraining(ctx context.Context, validate func(current ServiceStatus) error, publishFrom ...orchestratorinfo.ServiceInfoStatus) (bool, error) {
	if d == nil || d.info == nil {
		d.StartDraining(ctx)

		return false, nil
	}

	d.info.statusMu.Lock()
	defer d.info.statusMu.Unlock()

	if validate != nil {
		if err := validate(d.info.status); err != nil {
			return false, err
		}
	}

	if d.info.status.Status == orchestratorinfo.ServiceInfoStatus_Draining {
		return false, nil
	}

	// Close admission before publishing Draining so new work is rejected before
	// the status transition becomes observable.
	d.StartDraining(ctx)

	if len(publishFrom) > 0 && !slices.Contains(publishFrom, d.info.status.Status) {
		return false, nil
	}

	return d.info.setStatusLocked(ctx, orchestratorinfo.ServiceInfoStatus_Draining), nil
}

func (d *DrainCoordinator) ForceStop(ctx context.Context) error {
	if d == nil || d.controller == nil {
		return nil
	}

	return d.controller.ForceStop(ctx)
}
