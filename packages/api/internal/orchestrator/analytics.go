package orchestrator

import (
	"context"
	"time"

	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	// syncAnalyticsTime if this value is updated, it should be correctly updated in analytics too.
	syncAnalyticsTime   = 10 * time.Minute
	oldSandboxThreshold = 30 * time.Minute // Threshold to consider a sandbox as old

	// reportTimeout is the timeout for the analytics report
	// This timeout is also set in the CloudRun for Analytics Collector, there it is 3 minutes.
	reportTimeout = 4 * time.Minute
)

func (o *Orchestrator) reportLongRunningSandboxes(ctx context.Context) {
	ticker := time.NewTicker(syncAnalyticsTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			zap.L().Info("Stopping node analytics reporting due to context cancellation")
			return
		case <-ticker.C:
			sandboxes := o.sandboxStore.Items(nil)
			longRunningSandboxes := make([]instance.Data, 0, len(sandboxes))
			for _, sandbox := range sandboxes {
				if time.Since(sandbox.StartTime) > oldSandboxThreshold {
					longRunningSandboxes = append(longRunningSandboxes, sandbox)
				}
			}

			sendAnalyticsForLongRunningSandboxes(ctx, o.analytics, longRunningSandboxes)
		}
	}
}

// sendAnalyticsForLongRunningSandboxes sends long-running instances event to analytics
func sendAnalyticsForLongRunningSandboxes(ctx context.Context, analytics *analyticscollector.Analytics, instances []instance.Data) {
	if len(instances) == 0 {
		zap.L().Debug("No long-running instances to report to analytics")
		return
	}

	childCtx, cancel := context.WithTimeout(ctx, syncAnalyticsTime)
	defer cancel()

	instanceIds := make([]string, len(instances))
	executionIds := make([]string, len(instances))
	for idx, i := range instances {
		instanceIds[idx] = i.SandboxID
		executionIds[idx] = i.ExecutionID
	}

	_, err := analytics.RunningInstances(childCtx,
		&analyticscollector.RunningInstancesEvent{
			InstanceIds:  instanceIds,
			ExecutionIds: executionIds,
			Timestamp:    timestamppb.Now(),
		},
	)
	if err != nil {
		zap.L().Error("error sending running instances event to analytics", zap.Error(err))
	}
}

func (o *Orchestrator) analyticsRemove(ctx context.Context, sandbox instance.Data, stateAction instance.StateAction) {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	duration := time.Since(sandbox.StartTime).Seconds()
	stopTime := time.Now()

	o.posthogClient.CreateAnalyticsTeamEvent(
		sandbox.TeamID.String(),
		"closed_instance", posthog.NewProperties().
			Set("instance_id", sandbox.SandboxID).
			Set("environment", sandbox.TemplateID).
			Set("state_action", stateAction).
			Set("duration", duration),
	)

	_, err := o.analytics.InstanceStopped(ctx, &analyticscollector.InstanceStoppedEvent{
		TeamId:        sandbox.TeamID.String(),
		EnvironmentId: sandbox.TemplateID,
		InstanceId:    sandbox.SandboxID,
		ExecutionId:   sandbox.ExecutionID,
		Timestamp:     timestamppb.New(stopTime),
		Duration:      float32(duration),
		CpuCount:      sandbox.VCpu,
		RamMb:         sandbox.RamMB,
		DiskSizeMb:    sandbox.TotalDiskSizeMB,
	})
	if err != nil {
		zap.L().Error("error sending Analytics event", zap.Error(err))
	}
}

func (o *Orchestrator) analyticsInsert(ctx context.Context, sandbox instance.Data, created bool) {
	ctx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	if created {
		// Run in separate goroutine to not block sandbox creation
		_, err := o.analytics.InstanceStarted(ctx, &analyticscollector.InstanceStartedEvent{
			InstanceId:    sandbox.SandboxID,
			ExecutionId:   sandbox.ExecutionID,
			EnvironmentId: sandbox.TemplateID,
			BuildId:       sandbox.BuildID.String(),
			TeamId:        sandbox.TeamID.String(),
			CpuCount:      sandbox.VCpu,
			RamMb:         sandbox.RamMB,
			DiskSizeMb:    sandbox.TotalDiskSizeMB,
			Timestamp:     timestamppb.Now(),
		})
		if err != nil {
			zap.L().Error("Error sending Analytics event", zap.Error(err))
		}
	}
}

func (o *Orchestrator) countersInsert(ctx context.Context, sandbox instance.Data, newlyCreated bool) {
	attributes := []attribute.KeyValue{
		telemetry.WithTeamID(sandbox.TeamID.String()),
	}

	if newlyCreated {
		o.createdCounter.Add(ctx, 1, metric.WithAttributes(attributes...))
	}

	o.sandboxCounter.Add(ctx, 1, metric.WithAttributes(attributes...))
}

func (o *Orchestrator) countersRemove(ctx context.Context, sandbox instance.Data, _ instance.StateAction) {
	attributes := []attribute.KeyValue{
		telemetry.WithTeamID(sandbox.TeamID.String()),
	}

	o.sandboxCounter.Add(ctx, -1, metric.WithAttributes(attributes...))
}
