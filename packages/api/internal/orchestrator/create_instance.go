package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"net/http"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const (
	maxNodeRetries       = 3
	leastBusyNodeTimeout = 60 * time.Second

	maxStartingInstancesPerNode = 3
)

var (
	sandboxCreateFailedError = fmt.Errorf("failed to create a new sandbox, if the problem persists, contact us")
)

func (o *Orchestrator) CreateSandbox(
	ctx context.Context,
	sandboxID,
	alias string,
	team authcache.AuthTeamInfo,
	build *models.EnvBuild,
	metadata,
	envVars map[string]string,
	startTime time.Time,
	endTime time.Time,
	timeout time.Duration,
	isResume bool,
	clientID *string,
	baseTemplateID string,
	autoPause bool,
) (*api.Sandbox, *api.APIError) {
	childCtx, childSpan := o.tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// Check if team has reached max instances
	err, releaseTeamSandboxReservation := o.instanceCache.Reserve(sandboxID, team.Team.ID, team.Tier.ConcurrentInstances)
	if err != nil {
		errMsg := fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", team.Team.ID, team.Tier.ConcurrentInstances)
		telemetry.ReportCriticalError(ctx, fmt.Errorf("%w (error: %w)", errMsg, err))

		return nil, &api.APIError{
			Code: http.StatusTooManyRequests,
			ClientMsg: fmt.Sprintf(
				"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
					"please contact us at 'https://e2b.dev/docs/getting-help'", team.Tier.ConcurrentInstances),
			Err: errMsg,
		}
	}

	telemetry.ReportEvent(childCtx, "Reserved sandbox for team")
	defer releaseTeamSandboxReservation()

	features, err := sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", build.FirecrackerVersion, err)

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to get build information for the template",
			Err:       errMsg,
		}
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			BaseTemplateId:     baseTemplateID,
			TemplateId:         *build.EnvID,
			Alias:              &alias,
			TeamId:             team.Team.ID.String(),
			BuildId:            build.ID.String(),
			SandboxId:          sandboxID,
			KernelVersion:      build.KernelVersion,
			FirecrackerVersion: build.FirecrackerVersion,
			EnvdVersion:        *build.EnvdVersion,
			Metadata:           metadata,
			EnvVars:            envVars,
			MaxSandboxLength:   team.Tier.MaxLengthHours,
			HugePages:          features.HasHugePages(),
			RamMb:              build.RAMMB,
			Vcpu:               build.Vcpu,
			Snapshot:           isResume,
			AutoPause:          &autoPause,
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *Node

	if isResume && clientID != nil {
		telemetry.ReportEvent(childCtx, "Placing sandbox on the node where the snapshot was taken")

		node, _ = o.nodes.Get(*clientID)
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
				Err:       sandboxCreateFailedError,
			}
		}

		if node == nil {
			node, err = o.getLeastBusyNode(childCtx, nodesExcluded)
			if err != nil {
				errMsg := fmt.Errorf("failed to get least busy node: %w", err)
				telemetry.ReportError(childCtx, errMsg)

				return nil, &api.APIError{
					Code:      http.StatusInternalServerError,
					ClientMsg: "Failed to get node to place sandbox on.",
					Err:       errMsg,
				}
			}
		}

		// To creating a lot of sandboxes at once on the same node
		node.sbxsInProgress.Insert(sandboxID, &sbxInProgress{
			MiBMemory: build.RAMMB,
			CPUs:      build.Vcpu,
		})

		_, err = node.Client.Sandbox.Create(childCtx, sbxRequest)
		// The request is done, we will either add it to the cache or remove it from the node
		if err == nil {
			// The sandbox was created successfully
			break
		}

		node.sbxsInProgress.Remove(sandboxID)

		log.Printf("failed to create sandbox '%s' on node '%s', attempt #%d: %v", sandboxID, node.Info.ID, attempt, utils.UnwrapGRPCError(err))

		// The node is not available, try again with another node
		node.createFails.Add(1)
		nodesExcluded[node.Info.ID] = node
		node = nil
		attempt += 1
	}

	// The build should be cached on the node now
	node.InsertBuild(build.ID.String())

	// The sandbox was created successfully, the resources will be counted in cache
	defer node.sbxsInProgress.Remove(sandboxID)

	telemetry.SetAttributes(childCtx, attribute.String("node.id", node.Info.ID))
	telemetry.ReportEvent(childCtx, "Created sandbox")

	sbx := api.Sandbox{
		ClientID:    node.Info.ID,
		SandboxID:   sandboxID,
		TemplateID:  *build.EnvID,
		Alias:       &alias,
		EnvdVersion: *build.EnvdVersion,
	}

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	instanceInfo := instance.NewInstanceInfo(
		&sbx,
		&team.Team.ID,
		&build.ID,
		metadata,
		time.Duration(team.Tier.MaxLengthHours)*time.Hour,
		startTime,
		endTime,
		build.Vcpu,
		*build.TotalDiskSizeMB,
		build.RAMMB,
		build.KernelVersion,
		build.FirecrackerVersion,
		*build.EnvdVersion,
		node.Info,
		autoPause,
	)

	cacheErr := o.instanceCache.Add(instanceInfo, true)
	if cacheErr != nil {
		errMsg := fmt.Errorf("error when adding instance to cache: %w", cacheErr)
		telemetry.ReportError(ctx, errMsg)

		deleted := o.DeleteInstance(childCtx, sbx.SandboxID, false)
		if !deleted {
			telemetry.ReportEvent(ctx, "instance wasn't found in cache when deleting")
		}

		return nil, &api.APIError{
			Code:      http.StatusInternalServerError,
			ClientMsg: "Failed to create sandbox",
			Err:       errMsg,
		}
	}

	return &sbx, nil
}

func (o *Orchestrator) getLeastBusyNode(parentCtx context.Context, nodesExcluded map[string]*Node) (leastBusyNode *Node, err error) {
	ctx, cancel := context.WithTimeout(parentCtx, leastBusyNodeTimeout)
	defer cancel()

	childCtx, childSpan := o.tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	for {
		select {
		case <-childCtx.Done():
			return nil, childCtx.Err()
		case <-time.After(10 * time.Millisecond):
			// If no node is available, wait for a bit and try again

			for _, node := range o.nodes.Items() {
				// The node might be nil if it was removed from the list while iterating
				if node == nil {
					continue
				}

				// If the node is not ready, skip it
				if node.Status() != api.NodeStatusReady {
					continue
				}

				// Skip already tried nodes
				if nodesExcluded[node.Info.ID] != nil {
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
		}
	}
}
