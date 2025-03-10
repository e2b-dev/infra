package orchestrator

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	analyticscollector "github.com/e2b-dev/infra/packages/api/internal/analytics_collector"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
)

const (
	// syncAnalyticsTime if this value is updated, it should be correctly updated in analytics too.
	syncAnalyticsTime = 3 * time.Minute
)

func (o *Orchestrator) startNodeAnalytics(ctx context.Context) {
	ticker := time.NewTicker(syncAnalyticsTime)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			o.reportNodesAnalytics(ctx)
		}
	}
}

func (o *Orchestrator) reportNodesAnalytics(parentCtx context.Context) {
	ctx, cancel := context.WithTimeout(parentCtx, syncAnalyticsTime)
	defer cancel()

	// Group running sandboxes by the Node.ID, split to work in parallel
	sandboxes := o.instanceCache.Items()
	sbxsByNode := make(map[string][]*instance.InstanceInfo)
	for _, sbx := range sandboxes {
		nodeID := sbx.Node.ID
		sbxsByNode[nodeID] = append(sbxsByNode[nodeID], sbx)
	}

	var wg sync.WaitGroup
	for _, ns := range sbxsByNode {
		wg.Add(1)
		go func() {
			defer wg.Done()

			reportNodeAnalytics(ctx, o.analytics, ns)
		}()
	}

	wg.Wait()
}

// reportNodeAnalytics sends running instances event to analytics, should be scoped by the node.
func reportNodeAnalytics(ctx context.Context, analytics *analyticscollector.Analytics, instances []*instance.InstanceInfo) {
	instanceIds := make([]string, len(instances))
	for idx, i := range instances {
		instanceIds[idx] = i.Instance.SandboxID
	}

	_, err := analytics.Client.RunningInstances(ctx, &analyticscollector.RunningInstancesEvent{InstanceIds: instanceIds, Timestamp: timestamppb.Now()})
	if err != nil {
		zap.L().Error("error sending running instances event to analytics", zap.Error(err))
	}
}
