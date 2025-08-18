package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/posthog/posthog-go"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodes"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

	nomadNodes, err := o.listNomadNodes(spanCtx)
	if err != nil {
		zap.L().Error("Error listing orchestrator nodes", zap.Error(err))
		return
	}

	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		o.syncLocalDiscoveredNodes(spanCtx, nomadNodes)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		o.syncClusterDiscoveredNodes(spanCtx)
	}()

	// Wait for nodes discovery to finish
	wg.Wait()

	// Sync state of all nodes currently in the pool
	syncNodesSpanCtx, syncNodesSpan := o.tracer.Start(spanCtx, "keep-in-sync-existing")
	defer syncNodesSpan.End()

	defer wg.Wait()
	for _, n := range o.nodes.Items() {
		wg.Add(1)
		go func() {
			defer wg.Done()

			// cluster and local nodes needs to by synced differently,
			// because each of them is taken from different source pool
			if n.ClusterID == uuid.Nil {
				err := o.syncNode(syncNodesSpanCtx, n, nomadNodes, instanceCache)
				if err != nil {
					zap.L().Error("Error syncing local node", zap.Error(err))
					err = n.CloseWithClient()
					if err != nil {
						zap.L().Error("Error closing grpc connection", zap.Error(err))
					}

					o.deregisterNode(n)
				}
			} else {
				err := o.syncClusterNode(syncNodesSpanCtx, n, instanceCache)
				if err != nil {
					zap.L().Error("Error syncing cluster node", zap.Error(err))
					// we are not closing grpc connection, because it is shared between all cluster nodes, and it's handled by the cluster
					err = n.Close()
					if err != nil {
						zap.L().Error("Error closing grpc connection", zap.Error(err))
					}

					o.deregisterNode(n)
				}
			}
		}()
	}
}

func (o *Orchestrator) syncLocalDiscoveredNodes(ctx context.Context, discovered []nodes.NomadServiceDiscovery) {
	// Connect local nodes that are not in the list, yet
	connectLocalSpanCtx, connectLocalSpan := o.tracer.Start(ctx, "keep-in-sync-connect-local-nodes")
	defer connectLocalSpan.End()

	var wg sync.WaitGroup
	defer wg.Wait()

	for _, n := range discovered {
		// If the node is not in the list, connect to it
		if o.GetNodeByNomadShortID(n.NomadNodeShortID) == nil {
			wg.Add(1)
			go func() {
				defer wg.Done()
				err := o.connectToNode(connectLocalSpanCtx, n)
				if err != nil {
					zap.L().Error("Error connecting to node", zap.Error(err))
				}
			}()
		}
	}
}

func (o *Orchestrator) syncClusterDiscoveredNodes(ctx context.Context) {
	var wg sync.WaitGroup
	defer wg.Wait()

	_, connectClusteredSpan := o.tracer.Start(ctx, "keep-in-sync-connect-clustered-nodes")
	defer connectClusteredSpan.End()

	// Connect clustered nodes that are not in the list, yet
	// We need to iterate over all clusters and their nodes
	for _, cluster := range o.clusters.GetClusters() {
		for _, n := range cluster.GetOrchestrators() {
			// If the node is not in the list, connect to it
			if o.GetNode(cluster.ID, n.NodeID) == nil {
				wg.Add(1)
				go func() {
					defer wg.Done()
					o.connectToClusterNode(ctx, cluster, n)
				}()
			}
		}
	}
}

func (o *Orchestrator) syncClusterNode(ctx context.Context, node *nodes.Node, instanceCache *instance.InstanceCache) error {
	ctx, childSpan := o.tracer.Start(ctx, "sync-cluster-node")
	telemetry.SetAttributes(ctx, telemetry.WithNodeID(node.ID), telemetry.WithClusterID(node.ClusterID))
	defer childSpan.End()

	cluster, clusterFound := o.clusters.GetClusterById(node.ClusterID)
	if !clusterFound {
		return fmt.Errorf("cluster not found")
	}

	_, found := cluster.GetInstanceByNodeID(node.ID)
	if !found {
		return fmt.Errorf("node instance not found")
	}

	// Unified call for syncing node state across different node types
	node.Sync(ctx, o.tracer, instanceCache)

	return nil
}

func (o *Orchestrator) syncNode(ctx context.Context, node *nodes.Node, discovered []nodes.NomadServiceDiscovery, instanceCache *instance.InstanceCache) error {
	ctx, childSpan := o.tracer.Start(ctx, "sync-node")
	telemetry.SetAttributes(ctx, telemetry.WithNodeID(node.ID))
	defer childSpan.End()

	found := false
	for _, activeNode := range discovered {
		if node.NomadNodeShortID == activeNode.NomadNodeShortID {
			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("node '%s' not found in the discovered nodes", node.NomadNodeShortID)
	}

	// Unified call for syncing node state across different node types
	node.Sync(ctx, o.tracer, instanceCache)

	return nil
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
			info.SandboxID,
			info.ExecutionID,
			info.TemplateID,
			info.VCpu,
			info.RamMB,
			info.TotalDiskSizeMB,
			stopTime,
			ct,
			duration,
		)

		node := o.GetNode(info.ClusterID, info.NodeID)
		if node == nil {
			zap.L().Error("failed to get node", logger.WithNodeID(info.NodeID))
			return fmt.Errorf("node '%s' not found", info.NodeID)
		}

		node.RemoveSandbox(info)
		o.dns.Remove(ctx, info.SandboxID, node.IPAddress)

		if ct == ClosePause {
			o.instanceCache.MarkAsPausing(info)

			err := o.PauseInstance(ctx, info, info.TeamID)
			if err != nil {
				info.PauseDone(err)

				return fmt.Errorf("failed to auto pause sandbox '%s': %w", info.SandboxID, err)
			}

			// We explicitly unmark as pausing here to avoid a race condition
			// where we are creating a new instance, and the pausing one is still in the pausing cache.
			o.instanceCache.UnmarkAsPausing(info)
			info.PauseDone(nil)
		} else {
			req := &orchestrator.SandboxDeleteRequest{SandboxId: info.SandboxID}
			client, ctx := node.GetClient(ctx)
			_, err := client.Sandbox.Delete(node.GetSandboxDeleteCtx(ctx, info.SandboxID, info.ExecutionID), req)
			if err != nil {
				return fmt.Errorf("failed to delete sandbox '%s': %w", info.SandboxID, err)
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
	executionID string,
	templateID string,
	cpuCount int64,
	ramMB int64,
	diskSizeMB int64,
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

	_, err := analytics.InstanceStopped(childCtx, &analyticscollector.InstanceStoppedEvent{
		TeamId:        teamID,
		EnvironmentId: templateID,
		InstanceId:    sandboxID,
		ExecutionId:   executionID,
		Timestamp:     timestamppb.New(stopTime),
		Duration:      float32(duration),
		CpuCount:      cpuCount,
		RamMb:         ramMB,
		DiskSizeMb:    diskSizeMB,
	})
	if err != nil {
		zap.L().Error("error sending Analytics event", zap.Error(err))
	}
}

func (o *Orchestrator) getInsertInstanceFunction(parentCtx context.Context, timeout time.Duration) func(info *instance.InstanceInfo, created bool) error {
	return func(info *instance.InstanceInfo, created bool) error {
		ctx, cancel := context.WithTimeout(parentCtx, timeout)
		defer cancel()

		sbxlogger.I(info).Debug("Inserting sandbox to cache hook",
			zap.Time("start_time", info.StartTime),
			zap.Time("end_time", info.GetEndTime()),
			zap.Bool("auto_pause", info.AutoPause.Load()),
		)

		node := o.GetNode(info.ClusterID, info.NodeID)
		if node == nil {
			zap.L().Error("failed to get node", logger.WithNodeID(info.NodeID))
		} else {
			node.AddSandbox(info)

			o.dns.Add(ctx, info.SandboxID, node.IPAddress)
		}

		o.teamMetricsObserver.Add(ctx, info.TeamID, created)

		if info.AutoPause.Load() {
			o.instanceCache.MarkAsPausing(info)
		}

		if created {
			// Run in separate goroutine to not block sandbox creation
			// Also use parentCtx to not cancel the request with this hook timeout
			go reportInstanceStartAnalytics(
				parentCtx,
				o.analytics,
				info.TeamID.String(),
				info.SandboxID,
				info.ExecutionID,
				info.TemplateID,
				info.BuildID.String(),
				info.VCpu,
				info.RamMB,
				info.TotalDiskSizeMB,
			)
		}

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
	executionID string,
	templateID string,
	buildID string,
	cpuCount int64,
	ramMB int64,
	diskSizeMB int64,
) {
	childCtx, cancel := context.WithTimeout(ctx, reportTimeout)
	defer cancel()

	_, err := analytics.InstanceStarted(childCtx, &analyticscollector.InstanceStartedEvent{
		InstanceId:    sandboxID,
		ExecutionId:   executionID,
		EnvironmentId: templateID,
		BuildId:       buildID,
		TeamId:        teamID,
		CpuCount:      cpuCount,
		RamMb:         ramMB,
		DiskSizeMb:    diskSizeMB,
		Timestamp:     timestamppb.Now(),
	})
	if err != nil {
		zap.L().Error("Error sending Analytics event", zap.Error(err))
	}
}
