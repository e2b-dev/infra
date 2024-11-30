package orchestrator

import (
	"context"
	"fmt"
	"time"

	"github.com/posthog/posthog-go"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) GetSandbox(sandboxID string) (*instance.InstanceInfo, error) {
	item, err := o.instanceCache.Get(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox '%s': %w", sandboxID, err)
	}

	sbx := item.Value()
	return &sbx, nil
}

// keepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) keepInSync(ctx context.Context, instanceCache *instance.InstanceCache) {
	for {
		childCtx, childSpan := o.tracer.Start(ctx, "keep-in-sync")
		nodes, err := o.listNomadNodes()
		if err != nil {
			o.logger.Errorf("Error listing nodes: %v", err)
			childSpan.End()

			continue
		}

		for _, node := range nodes {
			// If the node is not in the list, connect to it
			_, err := o.GetNode(node.ID)
			if err != nil {
				err = o.connectToNode(node)
				if err != nil {
					o.logger.Errorf("Error connecting to node\n: %v", err)
				}
			}
		}

		// TODO: Maybe we should remove nodes that are not in the list anymore
		for _, node := range o.nodes {
			found := false
			for _, activeNode := range nodes {
				if node.ID == activeNode.ID {
					found = true
					break
				}
			}

			if !found {
				o.logger.Infof("Node %s is not active anymore", node.ID)

				// Close the connection to the node
				err = node.Client.Close()
				if err != nil {
					o.logger.Errorf("Error closing connection to node\n: %v", err)
				}

				delete(o.nodes, node.ID)
			}

			activeInstances, instancesErr := o.getInstances(childCtx, node.ID)
			if instancesErr != nil {
				o.logger.Errorf("Error getting instances\n: %v", instancesErr)
				continue
			}

			instanceCache.Sync(activeInstances, node.ID)

			o.logger.Infof("Node %s: CPU: %d, RAM: %d", node.ID, node.CPUUsage, node.RamUsage)
		}

		childSpan.End()

		// Sleep for a while before syncing again
		time.Sleep(instance.CacheSyncTime)
	}
}

func (o *Orchestrator) getDeleteInstanceFunction(ctx context.Context, posthogClient *analyticscollector.PosthogClient, logger *zap.SugaredLogger) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		duration := time.Since(info.StartTime).Seconds()

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

		node, err := o.GetNode(info.Instance.ClientID)
		if err != nil {
			return fmt.Errorf("failed to get node '%s': %w", info.Instance.ClientID, err)
		}

		node.CPUUsage -= info.VCpu
		node.RamUsage -= info.RamMB

		_, err = node.Client.Sandbox.Delete(ctx, &orchestrator.SandboxDeleteRequest{SandboxId: info.Instance.SandboxID})
		if err != nil {
			return fmt.Errorf("failed to delete sandbox '%s': %w", info.Instance.SandboxID, err)
		}

		logger.Infof("Closed sandbox '%s' after %f seconds", info.Instance.SandboxID, duration)

		return nil
	}
}

func (o *Orchestrator) getInsertInstanceFunction(ctx context.Context, logger *zap.SugaredLogger) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		node, err := o.GetNode(info.Instance.ClientID)
		if err != nil {
			return fmt.Errorf("failed to get node '%s': %w", info.Instance.ClientID, err)
		}
		node.CPUUsage += info.VCpu
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
			logger.Errorf("Error sending Analytics event: %v", err)
			telemetry.ReportCriticalError(ctx, errMsg)
		}

		logger.Infof("Created sandbox '%s'", info.Instance.SandboxID)

		return nil
	}
}
