package orchestrator

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/node"
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
func (o *Orchestrator) keepInSync(instanceCache *instance.InstanceCache) {
	for {
		ctx, span := o.tracer.Start(context.Background(), "keep-in-sync")
		nodes, err := o.listNomadNodes(ctx)
		if err != nil {
			o.logger.Errorf("Error listing nodes: %v", err)
			span.End()

			continue
		}

		var wg sync.WaitGroup
		for _, n := range nodes {
			// If the node is not in the list, connect to it
			if o.GetNode(n.ID) == nil {
				wg.Add(1)
				go func(n *node.NodeInfo) {
					defer wg.Done()
					err = o.connectToNode(ctx, n)
					if err != nil {
						o.logger.Errorf("Error connecting to node: %v", err)
					}
				}(n)
			}
		}
		wg.Wait()

		for _, n := range o.nodes.Items() {
			wg.Add(1)
			go func(n *Node) {
				defer wg.Done()
				o.syncNode(ctx, n, nodes, instanceCache)
			}(n)
		}
		wg.Wait()

		span.End()
		// Sleep for a while before syncing again
		time.Sleep(instance.CacheSyncTime)
	}
}

func (o *Orchestrator) syncNode(ctx context.Context, node *Node, nodes []*node.NodeInfo, instanceCache *instance.InstanceCache) {
	ctx, childSpan := o.tracer.Start(ctx, "sync-node")
	telemetry.SetAttributes(ctx, attribute.String("node.id", node.Info.ID))
	defer childSpan.End()

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
		err := node.Client.Close()
		if err != nil {
			o.logger.Errorf("Error closing connection to node: %v", err)
		}

		o.nodes.Remove(node.Info.ID)

		return
	}

	activeInstances, instancesErr := o.getSandboxes(ctx, node.Info)
	if instancesErr != nil {
		o.logger.Errorf("Error getting instances: %v", instancesErr)
		return
	}

	instanceCache.Sync(activeInstances, node.Info.ID)

	builds, buildsErr := o.listCachedBuilds(ctx, node.Info.ID)
	if buildsErr != nil {
		o.logger.Errorf("Error listing cached builds: %v", buildsErr)
		return
	}

	node.SyncBuilds(builds)
}

func (o *Orchestrator) getDeleteInstanceFunction(
	parentCtx context.Context,
	posthogClient *analyticscollector.PosthogClient,
	logger *zap.SugaredLogger,
	timeout time.Duration,
) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		defer o.instanceCache.UnmarkAsPausing(&info)

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

		var closeType string
		if *info.AutoPause {
			closeType = "pause"
		} else {
			closeType = "delete"
		}

		posthogClient.CreateAnalyticsTeamEvent(
			info.TeamID.String(),
			"closed_instance", posthog.NewProperties().
				Set("instance_id", info.Instance.SandboxID).
				Set("environment", info.Instance.TemplateID).
				Set("type", closeType).
				Set("duration", duration),
		)

		node := o.GetNode(info.Instance.ClientID)
		if node == nil {
			logger.Errorf("failed to get node '%s'", info.Instance.ClientID)
		} else {
			node.CPUUsage.Add(-info.VCpu)
			node.RamUsage.Add(-info.RamMB)

			o.dns.Remove(ctx, info.Instance.SandboxID, node.Info.IPAddress)
		}

		if node == nil {
			log.Printf("node '%s' not found", info.Instance.ClientID)
			return fmt.Errorf("node '%s' not found", info.Instance.ClientID)
		}

		if node.Client == nil {
			log.Printf("client for node '%s' not found", info.Instance.ClientID)
			return fmt.Errorf("client for node '%s' not found", info.Instance.ClientID)
		}

		if *info.AutoPause {
			o.instanceCache.MarkAsPausing(&info)

			err = o.PauseInstance(ctx, o.tracer, &info, *info.TeamID)
			if err != nil {
				info.PauseDone(err)
				return fmt.Errorf("failed to auto pause sandbox '%s': %w", info.Instance.SandboxID, err)
			}

			// We explicitly unmark as pausing here to avoid a race condition
			// where we are creating a new instance, and the pausing one is still in the pausing cache.
			o.instanceCache.UnmarkAsPausing(&info)
			info.PauseDone(nil)
		} else {
			req := &orchestrator.SandboxDeleteRequest{SandboxId: info.Instance.SandboxID}
			_, err = node.Client.Sandbox.Delete(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to delete sandbox '%s': %w", info.Instance.SandboxID, err)
			}
		}

		return nil
	}
}

func (o *Orchestrator) getInsertInstanceFunction(parentCtx context.Context, logger *zap.SugaredLogger, timeout time.Duration) func(info instance.InstanceInfo) error {
	return func(info instance.InstanceInfo) error {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		node := o.GetNode(info.Instance.ClientID)
		if node == nil {
			logger.Errorf("failed to get node '%s'", info.Instance.ClientID)
		} else {
			node.CPUUsage.Add(info.VCpu)
			node.RamUsage.Add(info.RamMB)

			o.dns.Add(ctx, info.Instance.SandboxID, node.Info.IPAddress)
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

		if *info.AutoPause {
			o.instanceCache.MarkAsPausing(&info)
		}

		return nil
	}
}
