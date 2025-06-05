package orchestrator

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
)

const (
	// syncAnalyticsTime if this value is updated, it should be correctly updated in analytics too.
	syncAnalyticsTime   = 10 * time.Minute
	oldSandboxThreshold = 30 * time.Minute // Threshold to consider a sandbox as old
)

func (o *Orchestrator) reportLongRunningSandboxes(parentCtx context.Context) {
	ticker := time.NewTicker(syncAnalyticsTime)
	defer ticker.Stop()

	for {
		select {
		case <-parentCtx.Done():
			zap.L().Info("Stopping node analytics reporting due to context cancellation")
			return
		case <-ticker.C:
			ctx, cancel := context.WithTimeout(parentCtx, syncAnalyticsTime)

			sandboxes := o.instanceCache.Items()
			longRunningSandboxes := make([]*instance.InstanceInfo, 0, len(sandboxes))
			for _, sandbox := range sandboxes {
				if time.Since(sandbox.StartTime) > oldSandboxThreshold {
					longRunningSandboxes = append(longRunningSandboxes, sandbox)
				}
			}

			sendAnalyticsForLongRunningSandboxes(ctx, o.analytics, longRunningSandboxes)
			cancel()
		}
	}
}

// sendAnalyticsForLongRunningSandboxes sends long-running instances event to analytics
func sendAnalyticsForLongRunningSandboxes(ctx context.Context, analytics *analyticscollector.Analytics, instances []*instance.InstanceInfo) {
	instanceIds := make([]string, len(instances))
	executionIds := make([]string, len(instances))
	for idx, i := range instances {
		instanceIds[idx] = i.Instance.SandboxID
		executionIds[idx] = i.ExecutionID
	}

	_, err := analytics.Client.RunningInstances(ctx, &analyticscollector.RunningInstancesEvent{InstanceIds: instanceIds, ExecutionIds: executionIds, Timestamp: timestamppb.Now()})
	if err != nil {
		zap.L().Error("error sending running instances event to analytics", zap.Error(err))
	}
}
