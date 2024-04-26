package orchestrator

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"time"

	"github.com/posthog/posthog-go"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) getDeleteInstanceFunction(ctx context.Context, posthogClient *analyticscollector.PosthogClient, logger *zap.SugaredLogger) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		duration := time.Since(*info.StartTime).Seconds()

		delErr := o.DeleteInstanceRequest(ctx, info.Instance.SandboxID, &info.Instance.ClientID)
		if delErr != nil {
			return fmt.Errorf("cannot delete instance '%s': %w", info.Instance.SandboxID, delErr)
		}

		if info.TeamID != nil && info.StartTime != nil {
			_, err := o.analytics.Client.InstanceStopped(ctx, &analyticscollector.InstanceStoppedEvent{
				TeamId:        info.TeamID.String(),
				EnvironmentId: info.Instance.TemplateID,
				InstanceId:    info.Instance.SandboxID,
				Timestamp:     timestamppb.Now(),
				Duration:      float32(duration),
			})
			if err != nil {
				logger.Errorf("error sending Analytics event: %v", err)
			}

			posthogClient.CreateAnalyticsTeamEvent(
				info.TeamID.String(),
				"closed_instance", posthog.NewProperties().
					Set("instance_id", info.Instance.SandboxID).
					Set("environment", info.Instance.TemplateID).
					Set("duration", duration),
			)
		}

		logger.Infof("Closed sandbox '%s' after %f seconds", info.Instance.SandboxID, duration)

		return nil
	}
}

func (o *Orchestrator) getInsertInstanceFunction(ctx context.Context) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		node, err := o.GetNode(info.Instance.ClientID)
		if err != nil {
			return fmt.Errorf("failed to get node '%s': %w", info.Instance.ClientID, err)
		}
		node.CPUUsage += info.VCPU
		node.RamUsage += info.RamMB

		_, err = o.analytics.Client.InstanceStarted(ctx, &analyticscollector.InstanceStartedEvent{
			InstanceId:    info.Instance.SandboxID,
			EnvironmentId: info.Instance.TemplateID,
			BuildId:       info.BuildID.String(),
			TeamId:        info.TeamID.String(),
			Timestamp:     timestamppb.Now(),
		})
		if err != nil {
			errMsg := fmt.Errorf("error when sending analytics event: %w", err)
			telemetry.ReportCriticalError(ctx, errMsg)
		}
		return nil
	}
}

func (o *Orchestrator) KeepAliveFor(sandboxID string, duration time.Duration) error {
	return o.instanceCache.KeepAliveFor(sandboxID, duration)
}

func (o *Orchestrator) ListSandboxes(teamID *uuid.UUID) []instance.InstanceInfo {
	return o.instanceCache.GetInstances(teamID)
}
