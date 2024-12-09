package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	authcache "github.com/e2b-dev/infra/packages/api/internal/cache/auth"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func getSandboxIDClient(sandboxID string) (string, bool) {
	parts := strings.Split(sandboxID, "-")
	if len(parts) != 2 {
		return "", false
	}

	return parts[1], true
}

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
	logger *logs.SandboxLogger,
	isResume bool,
) (*api.Sandbox, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// Check if team has reached max instances
	err, releaseTeamSandboxReservation := o.instanceCache.Reserve(sandboxID, team.Team.ID, team.Tier.ConcurrentInstances)
	if err != nil {
		errMsg := fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", team.Team.ID, team.Tier.ConcurrentInstances)
		telemetry.ReportCriticalError(ctx, fmt.Errorf("%w (error: %w)", errMsg, err))

		return nil, fmt.Errorf(
			"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
				"please contact us at 'https://e2b.dev/docs/getting-help'", team.Tier.ConcurrentInstances)
	}

	defer releaseTeamSandboxReservation()

	features, err := sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", build.FirecrackerVersion, err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
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
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *Node

	var excludedNodes []string

	for {
		if isResume {
			telemetry.ReportEvent(childCtx, "Placing sandbox on the node where the snapshot was taken")

			clientID, ok := getSandboxIDClient(sandboxID)
			if !ok {
				return nil, fmt.Errorf("failed to get client ID from sandbox ID '%s'", sandboxID)
			}

			node = o.nodes[clientID]
		} else {
			node = o.getLeastBusyNode(childCtx, excludedNodes...)
			telemetry.ReportEvent(childCtx, "Trying to place sandbox on node")
		}

		if node == nil {
			return nil, fmt.Errorf("failed to find a node to place sandbox on")
		}

		_, err = node.Client.Sandbox.Create(ctx, sbxRequest)
		if err == nil {
			break
		}

		err = utils.UnwrapGRPCError(err)
		if err != nil {
			if node.Client.connection.GetState() != connectivity.Ready {
				telemetry.ReportEvent(childCtx, "Placing sandbox on node failed, node not ready", attribute.String("node.id", node.ID))
				excludedNodes = append(excludedNodes, node.ID)
			} else {
				return nil, fmt.Errorf("failed to create sandbox on node '%s': %w", node.ID, err)
			}
		}
	}

	telemetry.SetAttributes(childCtx, attribute.String("node.id", node.ID))
	telemetry.ReportEvent(childCtx, "Created sandbox")

	sbx := api.Sandbox{
		ClientID:    node.ID,
		SandboxID:   sandboxID,
		TemplateID:  *build.EnvID,
		Alias:       &alias,
		EnvdVersion: *build.EnvdVersion,
	}

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	instanceInfo := instance.InstanceInfo{
		Logger:             logger,
		StartTime:          startTime,
		EndTime:            endTime,
		Instance:           &sbx,
		BuildID:            &build.ID,
		TeamID:             &team.Team.ID,
		Metadata:           metadata,
		VCpu:               build.Vcpu,
		RamMB:              build.RAMMB,
		TotalDiskSizeMB:    *build.TotalDiskSizeMB,
		KernelVersion:      build.KernelVersion,
		FirecrackerVersion: build.FirecrackerVersion,
		EnvdVersion:        *build.EnvdVersion,
		MaxInstanceLength:  time.Duration(team.Tier.MaxLengthHours) * time.Hour,
	}
	if cacheErr := o.instanceCache.Add(instanceInfo, true); cacheErr != nil {
		errMsg := fmt.Errorf("error when adding instance to cache: %w", cacheErr)
		telemetry.ReportError(ctx, errMsg)

		deleted := o.DeleteInstance(childCtx, sbx.SandboxID)
		if !deleted {
			telemetry.ReportEvent(ctx, "instance wasn't found in cache when deleting")
		}

		return nil, errMsg
	}

	return &sbx, nil
}

func (o *Orchestrator) getLeastBusyNode(ctx context.Context, excludedNodes ...string) (leastBusyNode *Node) {
	childCtx, childSpan := o.tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	for _, node := range o.nodes {
		if leastBusyNode == nil || node.CPUUsage < leastBusyNode.CPUUsage {
			for _, excludedNode := range excludedNodes {
				if node.ID == excludedNode {
					continue
				}
			}

			leastBusyNode = node
		}
	}

	telemetry.ReportEvent(childCtx, "found the least busy node")

	return leastBusyNode
}
