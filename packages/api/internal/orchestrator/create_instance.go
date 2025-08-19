package orchestrator

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const (
	maxNodeRetries       = 3
	leastBusyNodeTimeout = 60 * time.Second

	maxStartingInstancesPerNode = 3
)

var errSandboxCreateFailed = fmt.Errorf("failed to create a new sandbox, if the problem persists, contact us")

func (o *Orchestrator) CreateSandbox(
	ctx context.Context,
	sandboxID,
	executionID,
	alias string,
	team authcache.AuthTeamInfo,
	build queries.EnvBuild,
	metadata map[string]string,
	envVars map[string]string,
	startTime time.Time,
	endTime time.Time,
	timeout time.Duration,
	isResume bool,
	nodeID *string,
	baseTemplateID string,
	autoPause bool,
	envdAuthToken *string,
	allowInternetAccess *bool,
) (*api.Sandbox, *api.APIError) {
	childCtx, childSpan := o.tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// Check if team has reached max instances
	releaseTeamSandboxReservation, err := o.instanceCache.Reserve(sandboxID, team.Team.ID, team.Tier.ConcurrentInstances)
	if err != nil {
		var limitErr *instance.ErrSandboxLimitExceeded
		var alreadyErr *instance.ErrAlreadyBeingStarted

		telemetry.ReportCriticalError(ctx, "failed to reserve sandbox for team", err)

		switch {
		case errors.As(err, &limitErr):
			return nil, &api.APIError{
				Code: http.StatusTooManyRequests,
				ClientMsg: fmt.Sprintf(
					"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
						"please contact us at 'https://e2b.dev/docs/getting-help'", team.Tier.ConcurrentInstances),
				Err: fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", team.Team.ID, team.Tier.ConcurrentInstances),
			}
		case errors.As(err, &alreadyErr):
			zap.L().Warn("sandbox already being started", logger.WithSandboxID(sandboxID), zap.Error(err))
			return nil, &api.APIError{
				Code:      http.StatusConflict,
				ClientMsg: fmt.Sprintf("Sandbox %s is already being started", sandboxID),
				Err:       err,
			}
		default:
			zap.L().Error("failed to reserve sandbox for team", logger.WithSandboxID(sandboxID), zap.Error(err))
			return nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: fmt.Sprintf("Failed to create sandbox: %s", err),
				Err:       err,
			}
		}
	}

	telemetry.ReportEvent(childCtx, "Reserved sandbox for team")
	defer releaseTeamSandboxReservation()

	_, err = sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", build.FirecrackerVersion, err)

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to get build information for the template",
			Err:       errMsg,
		}
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	var sbxDomain *string
	if team.Team.ClusterID != nil {
		cluster, ok := o.clusters.GetClusterById(*team.Team.ClusterID)
		if !ok {
			return nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Error while looking for sandbox cluster information",
				Err:       fmt.Errorf("cannot access cluster %s associated with team id %s that spawned sandbox %s", *team.Team.ClusterID, team.Team.ID, sandboxID),
			}
		}

		sbxDomain = cluster.SandboxDomain
	}

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			BaseTemplateId:      baseTemplateID,
			TemplateId:          *build.EnvID,
			Alias:               &alias,
			TeamId:              team.Team.ID.String(),
			BuildId:             build.ID.String(),
			SandboxId:           sandboxID,
			ExecutionId:         executionID,
			KernelVersion:       build.KernelVersion,
			FirecrackerVersion:  build.FirecrackerVersion,
			EnvdVersion:         *build.EnvdVersion,
			Metadata:            metadata,
			EnvVars:             envVars,
			EnvdAccessToken:     envdAuthToken,
			MaxSandboxLength:    team.Tier.MaxLengthHours,
			HugePages:           false,
			RamMb:               build.RamMb,
			Vcpu:                build.Vcpu,
			Snapshot:            isResume,
			AutoPause:           autoPause,
			AllowInternetAccess: allowInternetAccess,
			TotalDiskSizeMb:     ut.FromPtr(build.TotalDiskSizeMb),
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *Node

	if isResume && nodeID != nil {
		telemetry.ReportEvent(childCtx, "Placing sandbox on the node where the snapshot was taken")

		clusterID := uuid.Nil
		if team.Team.ClusterID != nil {
			clusterID = *team.Team.ClusterID
		}

		node = o.GetNode(clusterID, *nodeID)
		if node != nil && node.Status() != api.NodeStatusReady {
			node = nil
		}
	}

	attempt := 1
	nodesExcluded := make(map[string]*Node)
	for {
		select {
		case <-childCtx.Done():
			return nil, &api.APIError{
				Code:      http.StatusRequestTimeout,
				ClientMsg: "Failed to create sandbox",
				Err:       fmt.Errorf("timeout while creating sandbox, attempt #%d", attempt),
			}
		default:
			// Continue
		}

		if attempt > maxNodeRetries {
			return nil, &api.APIError{
				Code:      http.StatusInternalServerError,
				ClientMsg: "Failed to create sandbox",
				Err:       errSandboxCreateFailed,
			}
		}

		if node == nil {
			nodeClusterID := uuid.Nil
			if team.Team.ClusterID != nil {
				nodeClusterID = *team.Team.ClusterID
			}

			node, err = o.getLeastBusyNode(childCtx, nodesExcluded, nodeClusterID)
			if err != nil {
				telemetry.ReportError(childCtx, "failed to get least busy node", err)

				return nil, &api.APIError{
					Code:      http.StatusInternalServerError,
					ClientMsg: "Failed to get node to place sandbox on.",
					Err:       fmt.Errorf("failed to get least busy node: %w", err),
				}
			}
		}

		// To creating a lot of sandboxes at once on the same node
		node.sbxsInProgress.Insert(sandboxID, &sbxInProgress{
			MiBMemory: build.RamMb,
			CPUs:      build.Vcpu,
		})

		client, childCtx := node.getClient(childCtx)
		_, err = client.Sandbox.Create(node.GetSandboxCreateCtx(childCtx, sbxRequest), sbxRequest)
		// The request is done, we will either add it to the cache or remove it from the node
		if err == nil {
			// The sandbox was created successfully
			attributes := []attribute.KeyValue{
				attribute.Int("attempts", attempt),
				attribute.Bool("is_resume", isResume),
				attribute.Bool("node_affinity_requested", nodeID != nil),
				attribute.Bool("node_affinity_success", nodeID != nil && node.Info.NodeID == *nodeID),
			}
			o.createdSandboxesCounter.Add(ctx, 1, metric.WithAttributes(attributes...))
			break
		}

		node.sbxsInProgress.Remove(sandboxID)

		zap.L().Error("Failed to create sandbox", logger.WithSandboxID(sandboxID), logger.WithNodeID(node.Info.NodeID), zap.Int("attempt", attempt), zap.Error(utils.UnwrapGRPCError(err)))

		// The node is not available, try again with another node
		node.createFails.Add(1)
		nodesExcluded[node.Info.NodeID] = node
		node = nil
		attempt += 1
	}

	// The build should be cached on the node now
	node.InsertBuild(build.ID.String())
	node.createSuccess.Add(1)

	// The sandbox was created successfully, the resources will be counted in cache
	defer node.sbxsInProgress.Remove(sandboxID)

	telemetry.SetAttributes(childCtx, attribute.String("node.id", node.Info.NodeID))
	telemetry.ReportEvent(childCtx, "Created sandbox")

	sbx := api.Sandbox{
		ClientID:        consts.ClientID,
		SandboxID:       sandboxID,
		TemplateID:      *build.EnvID,
		Alias:           &alias,
		EnvdVersion:     *build.EnvdVersion,
		EnvdAccessToken: envdAuthToken,
		Domain:          sbxDomain,
	}

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	instanceInfo := instance.NewInstanceInfo(
		sbx.SandboxID,
		sbx.TemplateID,
		sbx.ClientID,
		sbx.Alias,
		executionID,
		team.Team.ID,
		build.ID,
		metadata,
		time.Duration(team.Tier.MaxLengthHours)*time.Hour,
		startTime,
		endTime,
		build.Vcpu,
		*build.TotalDiskSizeMb,
		build.RamMb,
		build.KernelVersion,
		build.FirecrackerVersion,
		*build.EnvdVersion,
		node.Info,
		autoPause,
		envdAuthToken,
		allowInternetAccess,
		baseTemplateID,
	)

	cacheErr := o.instanceCache.Add(childCtx, instanceInfo, true)
	if cacheErr != nil {
		telemetry.ReportError(ctx, "error when adding instance to cache", cacheErr)

		deleted := o.DeleteInstance(childCtx, sbx.SandboxID, false)
		if !deleted {
			telemetry.ReportEvent(ctx, "instance wasn't found in cache when deleting")
		}

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to create sandbox",
			Err:       fmt.Errorf("error when adding instance to cache: %w", cacheErr),
		}
	}

	return &sbx, nil
}

// getLeastBusyNode returns the least busy node, if there are no eligible nodes, it tries until one is available or the context timeouts
func (o *Orchestrator) getLeastBusyNode(parentCtx context.Context, nodesExcluded map[string]*Node, clusterID uuid.UUID) (leastBusyNode *Node, err error) {
	ctx, cancel := context.WithTimeout(parentCtx, leastBusyNodeTimeout)
	defer cancel()

	childCtx, childSpan := o.tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	// Try to find a node without waiting
	leastBusyNode, err = o.findLeastBusyNode(nodesExcluded, clusterID)
	if err == nil {
		return leastBusyNode, nil
	}

	// If no node is available, wait for a bit and try again
	ticker := time.NewTicker(10 * time.Millisecond)
	for {
		select {
		case <-childCtx.Done():
			return nil, childCtx.Err()
		case <-ticker.C:
			// If no node is available, wait for a bit and try again
			leastBusyNode, err = o.findLeastBusyNode(nodesExcluded, clusterID)
			if err == nil {
				return leastBusyNode, nil
			}
		}
	}
}

// findLeastBusyNode finds the least busy node that is ready and not in the excluded list
// if no node is available, returns an error
func (o *Orchestrator) findLeastBusyNode(nodesExcluded map[string]*Node, clusterID uuid.UUID) (leastBusyNode *Node, err error) {
	for _, node := range o.nodes.Items() {
		// The node might be nil if it was removed from the list while iterating
		if node == nil {
			continue
		}

		// Node must be in the same cluster as requested
		if node.Info.ClusterID != clusterID {
			continue
		}

		// If the node is not ready, skip it
		if node.Status() != api.NodeStatusReady {
			continue
		}

		// Skip already tried nodes
		if nodesExcluded[node.Info.NodeID] != nil {
			continue
		}

		// To prevent overloading the node
		if node.sbxsInProgress.Count() > maxStartingInstancesPerNode {
			continue
		}

		cpuUsage := int64(0)
		for _, sbx := range node.sbxsInProgress.Items() {
			cpuUsage += sbx.CPUs
		}

		if leastBusyNode == nil || (node.CPUUsage.Load()+cpuUsage) < leastBusyNode.CPUUsage.Load() {
			leastBusyNode = node
		}
	}

	if leastBusyNode != nil {
		return leastBusyNode, nil
	}

	return nil, fmt.Errorf("no node available")
}
