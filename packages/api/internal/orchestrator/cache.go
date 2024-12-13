package orchestrator

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/posthog/posthog-go"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
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
			if o.GetNode(node.ID) == nil {
				err = o.connectToNode(node)
				if err != nil {
					o.logger.Errorf("Error connecting to node\n: %v", err)
				}
			}
		}

		for _, node := range o.nodes {
			found := false
			for _, activeNode := range nodes {
				if node.Info.ID == activeNode.ID {
					found = true
					break
				}
			}

			if !found {
				o.logger.Infof("Node %s is not active anymore", node.Info.ID)

				// Close the connection to the node
				err = node.Client.Close()
				if err != nil {
					o.logger.Errorf("Error closing connection to node\n: %v", err)
				}

				delete(o.nodes, node.Info.ID)
				continue
			}

			activeInstances, instancesErr := o.getInstances(childCtx, node.Info)
			if instancesErr != nil {
				o.logger.Errorf("Error getting instances\n: %v", instancesErr)
				continue
			}

			instanceCache.Sync(activeInstances, node.Info.ID)

			go func() {
				builds, buildsErr := o.listCachedBuilds(childCtx, node.Info.ID)
				if buildsErr != nil {
					o.logger.Errorf("Error listing cached builds\n: %v", buildsErr)
					return
				}

				node.SyncBuilds(builds)
			}()

			o.logger.Infof("Node %s: CPU: %d, RAM: %d", node.Info.ID, node.CPUUsage, node.RamUsage)
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

		node := o.GetNode(info.Instance.ClientID)
		if node == nil {
			logger.Errorf("failed to get node '%s'", info.Instance.ClientID)
		} else {
			node.CPUUsage -= info.VCpu
			node.RamUsage -= info.RamMB

			o.dns.Remove(info.Instance.SandboxID)
		}

		req := &orchestrator.SandboxDeleteRequest{SandboxId: info.Instance.SandboxID}
		if node == nil {
			log.Printf("node '%s' not found", info.Instance.ClientID)
			return fmt.Errorf("node '%s' not found", info.Instance)
		}

		if node.Client == nil {
			log.Printf("client for node '%s' not found", info.Instance.ClientID)
			return fmt.Errorf("client for node '%s' not found", info.Instance)
		}

		_, err = node.Client.Sandbox.Delete(ctx, req)
		if err != nil {
			return fmt.Errorf("failed to delete sandbox '%s': %w", info.Instance.SandboxID, err)
		}

		return nil
	}
}

func (o *Orchestrator) getInsertInstanceFunction(ctx context.Context, logger *zap.SugaredLogger) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		node := o.GetNode(info.Instance.ClientID)
		if node == nil {
			logger.Errorf("failed to get node '%s'", info.Instance.ClientID)
		} else {
			node.CPUUsage += info.VCpu
			node.RamUsage += info.RamMB

			o.dns.Add(info.Instance.SandboxID, node.Info.IPAddress)
		}

		_, err := o.analytics.Client.InstanceStarted(ctx, &analyticscollector.InstanceStartedEvent{
			InstanceId:    info.Instance.SandboxID,
			EnvironmentId: info.Instance.TemplateID,
			BuildId:       info.BuildID.String(),
			TeamId:        info.TeamID.String(),
			Timestamp:     timestamppb.Now(),
		})
		if err != nil {
			logger.Errorf("Error sending Analytics event: %v", err)
		}

		return nil
	}
}
