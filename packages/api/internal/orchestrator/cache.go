package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/posthog/posthog-go"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

// cacheSyncTime is the time to sync the cache with the actual instances in Orchestrator.
const cacheSyncTime = 20 * time.Second

// reportTimeout is the timeout for the analytics report
// This timeout is also set in the CloudRun for Analytics Collector, there it is 3 minutes.
const reportTimeout = 4 * time.Minute

type closeType string

const (
	syncMaxRetries = 4

	ClosePause  closeType = "pause"
	CloseDelete closeType = "delete"
)

func (o *Orchestrator) GetSandbox(sandboxID string) (*instance.InstanceInfo, error) {
	item, err := o.instanceCache.Get(sandboxID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sandbox '%s': %w", sandboxID, err)
	}

	return item, nil
}

// keepInSync the cache with the actual instances in Orchestrator to handle instances that died.
func (o *Orchestrator) keepInSync(ctx context.Context, instanceCache *instance.InstanceCache) {
	// Run the first sync immediately
	zap.L().Info("Running the initial node sync")
	o.syncNodes(ctx, instanceCache)

	// Sync the nodes every cacheSyncTime
	ticker := time.NewTicker(cacheSyncTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			zap.L().Info("Stopping keepInSync")

			return
		case <-ticker.C:
			o.syncNodes(ctx, instanceCache)
		}
	}
}

func (o *Orchestrator) syncNodes(ctx context.Context, instanceCache *instance.InstanceCache) {
	ctxTimeout, cancel := context.WithTimeout(ctx, cacheSyncTime)
	defer cancel()

	spanCtx, span := o.tracer.Start(ctxTimeout, "keep-in-sync")
	defer span.End()

	nodes, err := o.listNomadNodes(spanCtx)
	if err != nil {
		zap.L().Error("Error listing nodes", zap.Error(err))

		return
	}

	var wg sync.WaitGroup
	for _, n := range nodes {
		// If the node is not in the list, connect to it
		if o.GetNode(n.ID) == nil {
			wg.Add(1)
			go func(n *node.NodeInfo) {
				defer wg.Done()
				err = o.connectToNode(spanCtx, n)
				if err != nil {
					zap.L().Error("Error connecting to node", zap.Error(err))
				}
			}(n)
		}
	}
	wg.Wait()

	for _, n := range o.nodes.Items() {
		wg.Add(1)
		go func(n *Node) {
			defer wg.Done()
			o.syncNode(spanCtx, n, nodes, instanceCache)
		}(n)
	}
	wg.Wait()
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
		zap.L().Info("Node is not active anymore", zap.String("node_id", node.Info.ID))

		// Close the connection to the node
		err := node.Client.Close()
		if err != nil {
			zap.L().Error("Error closing connection to node", zap.Error(err))
		}

		o.nodes.Remove(node.Info.ID)

		return
	}

	syncRetrySuccess := false

	for range syncMaxRetries {
		nodeInfo, err := node.Client.Info.ServiceInfo(ctx, &emptypb.Empty{})
		if err != nil {
			zap.L().Error("Error getting node info", zap.Error(err))
			continue
		}

		// update node status (if changed)
		node.SetStatus(o.getNodeStatusConverted(nodeInfo.ServiceStatus))

		activeInstances, instancesErr := o.getSandboxes(ctx, node.Info)
		if instancesErr != nil {
			zap.L().Error("Error getting instances", zap.Error(instancesErr))
			continue
		}

		instanceCache.Sync(ctx, activeInstances, node.Info.ID)

		syncRetrySuccess = true
		break
	}

	if !syncRetrySuccess {
		zap.L().Error("Failed to sync node after max retries, temporarily marking as draining", zap.String("node_id", node.Info.ID))
		node.SetStatus(api.NodeStatusDraining)
		return
	}

	builds, buildsErr := o.listCachedBuilds(ctx, node.Info.ID)
	if buildsErr != nil {
		zap.L().Error("Error listing cached builds", zap.Error(buildsErr))
		return
	}

	node.SyncBuilds(builds)
}

func (o *Orchestrator) getDeleteInstanceFunction(
	parentCtx context.Context,
	posthogClient *analyticscollector.PosthogClient,
	timeout time.Duration,
) func(info *instance.InstanceInfo) error {
	return func(info *instance.InstanceInfo) error {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		sbxlogger.I(info).Debug("Deleting sandbox from cache hook",
			zap.Time("start_time", info.StartTime),
			zap.Time("end_time", info.GetEndTime()),
			zap.Bool("auto_pause", info.AutoPause.Load()),
		)

		defer o.instanceCache.UnmarkAsPausing(info)

		duration := time.Since(info.StartTime).Seconds()
		stopTime := time.Now()

		var ct closeType
		if info.AutoPause.Load() {
			ct = ClosePause
		} else {
			ct = CloseDelete
		}

		// Run in separate goroutine to not block sandbox deletion
		// Also use parentCtx to not cancel the request with this hook timeout
		go reportInstanceStopAnalytics(
			parentCtx,
			posthogClient,
			o.analytics,
			info.TeamID.String(),
			info.Instance.SandboxID,
			info.Instance.TemplateID,
			stopTime,
			ct,
			duration,
		)

		node := o.GetNode(info.Instance.ClientID)
		if node == nil {
			zap.L().Error("failed to get node", zap.String("node_id", info.Instance.ClientID))

			return fmt.Errorf("node '%s' not found", info.Instance.ClientID)
		}

		node.CPUUsage.Add(-info.VCpu)
		node.RamUsage.Add(-info.RamMB)

		o.dns.Remove(ctx, info.Instance.SandboxID, node.Info.IPAddress)

		if node.Client == nil {
			zap.L().Error("client for node not found", zap.String("node_id", info.Instance.ClientID))

			return fmt.Errorf("client for node '%s' not found", info.Instance.ClientID)
		}

		if ct == ClosePause {
			o.instanceCache.MarkAsPausing(info)

			err := o.PauseInstance(ctx, o.tracer, info, *info.TeamID)
			if err != nil {
				info.PauseDone(err)

				return fmt.Errorf("failed to auto pause sandbox '%s': %w", info.Instance.SandboxID, err)
			}

			// We explicitly unmark as pausing here to avoid a race condition
			// where we are creating a new instance, and the pausing one is still in the pausing cache.
			o.instanceCache.UnmarkAsPausing(info)
			info.PauseDone(nil)
		} else {
			req := &orchestrator.SandboxDeleteRequest{SandboxId: info.Instance.SandboxID}
			_, err := node.Client.Sandbox.Delete(ctx, req)
			if err != nil {
				return fmt.Errorf("failed to delete sandbox '%s': %w", info.Instance.SandboxID, err)
			}
		}

		sbxlogger.I(info).Debug("Deleted sandbox from cache hook",
			zap.Time("start_time", info.StartTime),
			zap.Time("end_time", info.GetEndTime()),
			zap.Bool("auto_pause", info.AutoPause.Load()),
		)

		return nil
	}
}

func reportInstanceStopAnalytics(
	ctx context.Context,
	posthogClient *analyticscollector.PosthogClient,
	analytics *analyticscollector.Analytics,
	teamID string,
	sandboxID string,
	templateID string,
	stopTime time.Time,
	ct closeType,
	duration float64,
) {
	childCtx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	posthogClient.CreateAnalyticsTeamEvent(
		teamID,
		"closed_instance", posthog.NewProperties().
			Set("instance_id", sandboxID).
			Set("environment", templateID).
			Set("type", ct).
			Set("duration", duration),
	)

	_, err := analytics.Client.InstanceStopped(childCtx, &analyticscollector.InstanceStoppedEvent{
		TeamId:        teamID,
		EnvironmentId: templateID,
		InstanceId:    sandboxID,
		Timestamp:     timestamppb.New(stopTime),
		Duration:      float32(duration),
	})
	if err != nil {
		zap.L().Error("error sending Analytics event", zap.Error(err))
	}
}

func (o *Orchestrator) getInsertInstanceFunction(parentCtx context.Context, timeout time.Duration) func(info *instance.InstanceInfo) error {
	return func(info *instance.InstanceInfo) error {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		sbxlogger.I(info).Debug("Inserting sandbox to cache hook",
			zap.Time("start_time", info.StartTime),
			zap.Time("end_time", info.GetEndTime()),
			zap.Bool("auto_pause", info.AutoPause.Load()),
		)

		node := o.GetNode(info.Instance.ClientID)
		if node == nil {
			zap.L().Error("failed to get node", zap.String("node_id", info.Instance.ClientID))
		} else {
			node.CPUUsage.Add(info.VCpu)
			node.RamUsage.Add(info.RamMB)

			o.dns.Add(ctx, info.Instance.SandboxID, node.Info.IPAddress)
		}

		if info.AutoPause.Load() {
			o.instanceCache.MarkAsPausing(info)
		}

		// Run in separate goroutine to not block sandbox creation
		// Also use parentCtx to not cancel the request with this hook timeout
		go reportInstanceStartAnalytics(
			parentCtx,
			o.analytics,
			info.TeamID.String(),
			info.Instance.SandboxID,
			info.Instance.TemplateID,
			info.BuildID.String(),
		)

		sbxlogger.I(info).Debug("Inserted sandbox to cache hook",
			zap.Time("start_time", info.StartTime),
			zap.Time("end_time", info.GetEndTime()),
			zap.Bool("auto_pause", info.AutoPause.Load()),
		)

		return nil
	}
}

func reportInstanceStartAnalytics(
	ctx context.Context,
	analytics *analyticscollector.Analytics,
	teamID string,
	sandboxID string,
	templateID string,
	buildID string,
) {
	childCtx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	_, err := analytics.Client.InstanceStarted(childCtx, &analyticscollector.InstanceStartedEvent{
		InstanceId:    sandboxID,
		EnvironmentId: templateID,
		BuildId:       buildID,
		TeamId:        teamID,
		Timestamp:     timestamppb.Now(),
	})
	if err != nil {
		zap.L().Error("Error sending Analytics event", zap.Error(err))
	}
}
