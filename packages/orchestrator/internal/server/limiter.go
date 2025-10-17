package server

import (
	"context"
	"fmt"

	"go.uber.org/zap"
	"golang.org/x/sync/semaphore"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/metrics"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Limiter struct {
	featureFlags      *featureflags.Client
	startingSandboxes *semaphore.Weighted
	metricsTracker    *metrics.Tracker
}

func NewLimiter(
	maxStartingSandboxes int64,
	featureFlags *featureflags.Client,
	metricsTracker *metrics.Tracker,
) *Limiter {
	return &Limiter{
		featureFlags:      featureFlags,
		metricsTracker:    metricsTracker,
		startingSandboxes: semaphore.NewWeighted(maxStartingSandboxes),
	}
}

type TooManySandboxesRunningError struct {
	Current, Max int
}

func (t TooManySandboxesRunningError) Error() string {
	return fmt.Sprintf("max number of running sandboxes on node reached (%d), please retry", t.Max)
}

var _ error = TooManySandboxesRunningError{}

type TooManySandboxesStartingError struct {
	Current, Max int
}

var _ error = TooManySandboxesStartingError{}

func (t TooManySandboxesStartingError) Error() string {
	return fmt.Sprintf("max number of starting sandboxes on node reached (%d), please retry", t.Max)
}

func (t *Limiter) AcquireStarting(ctx context.Context) error {
	maxRunningSandboxesPerNode, err := t.featureFlags.IntFlag(ctx, featureflags.MaxSandboxesPerNode)
	if err != nil {
		zap.L().Error("Failed to get MaxSandboxesPerNode flag", zap.Error(err))
	}

	runningSandboxes := t.metricsTracker.TotalRunningCount()
	if runningSandboxes >= maxRunningSandboxesPerNode {
		telemetry.ReportEvent(ctx, "max number of running sandboxes reached")

		return TooManySandboxesRunningError{runningSandboxes, maxRunningSandboxesPerNode}
	}

	// Check if we've reached the max number of starting instances on this node
	acquired := t.startingSandboxes.TryAcquire(1)
	if !acquired {
		telemetry.ReportEvent(ctx, "too many starting sandboxes on node")
		return TooManySandboxesStartingError{runningSandboxes, maxRunningSandboxesPerNode}
	}

	return nil
}

func (t *Limiter) ReleaseStarting() {
	defer t.startingSandboxes.Release(1)
}
