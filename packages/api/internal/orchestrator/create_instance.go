package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/connectivity"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logs"
	"github.com/e2b-dev/infra/packages/shared/pkg/models"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) CreateSandbox(
	ctx context.Context,
	sandboxID,
	templateID,
	alias string,
	teamID uuid.UUID,
	build *models.EnvBuild,
	maxInstanceLengthHours int64,
	metadata,
	envVars map[string]string,
	kernelVersion,
	firecrackerVersion,
	envdVersion string,
	startTime time.Time,
	endTime time.Time,
	maxInstancesPerTeam int64,
	timeout time.Duration,
	logger *logs.SandboxLogger,
) (*api.Sandbox, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "create-sandbox",
		trace.WithAttributes(
			attribute.String("env.id", templateID),
		),
	)
	defer childSpan.End()

	// Check if team has reached max instances
	err, releaseTeamSandboxReservation := o.instanceCache.Reserve(sandboxID, teamID, maxInstancesPerTeam)
	if err != nil {
		errMsg := fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", teamID, maxInstancesPerTeam)
		telemetry.ReportCriticalError(ctx, fmt.Errorf("%w (error: %w)", errMsg, err))

		return nil, fmt.Errorf(
			"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
				"please contact us at 'https://e2b.dev/docs/getting-help'", maxInstancesPerTeam)
	}

	defer releaseTeamSandboxReservation()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)

		return nil, errMsg
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	sbxRequest := &orchestrator.SandboxCreateRequest{
		Sandbox: &orchestrator.SandboxConfig{
			TemplateId:         templateID,
			Alias:              &alias,
			TeamId:             teamID.String(),
			BuildId:            build.ID.String(),
			SandboxId:          sandboxID,
			KernelVersion:      kernelVersion,
			FirecrackerVersion: firecrackerVersion,
			EnvdVersion:        envdVersion,
			Metadata:           metadata,
			EnvVars:            envVars,
			MaxSandboxLength:   maxInstanceLengthHours,
			HugePages:          features.HasHugePages(),
			RamMb:              build.RAMMB,
			Vcpu:               build.Vcpu,
		},
		StartTime: timestamppb.New(startTime),
		EndTime:   timestamppb.New(endTime),
	}

	var node *Node

	var excludedNodes []string

	for {
		if childCtx.Err() != nil {
			return nil, fmt.Errorf("context was canceled")
		}

		node = o.getLeastBusyNode(childCtx, excludedNodes...)
		telemetry.ReportEvent(childCtx, "Trying to place sandbox on node")

		if node == nil {
			continue
		}

		node.CPUUsage += build.Vcpu
		node.RamUsage += build.RAMMB
		node.sbxInProgress++

		_, err = node.Client.Sandbox.Create(ctx, sbxRequest)
		if err == nil {
			break
		}

		node.CPUUsage -= build.Vcpu
		node.RamUsage -= build.RAMMB
		node.sbxInProgress--

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
		TemplateID:  templateID,
		Alias:       &alias,
		EnvdVersion: *build.EnvdVersion,
	}

	// This is to compensate for the time it takes to start the instance
	// Otherwise it could cause the instance to expire before user has a chance to use it
	startTime = time.Now()
	endTime = startTime.Add(timeout)

	node.CPUUsage -= build.Vcpu
	node.RamUsage -= build.RAMMB
	node.sbxInProgress--

	instanceInfo := instance.InstanceInfo{
		Logger:            logger,
		StartTime:         startTime,
		EndTime:           endTime,
		Instance:          &sbx,
		BuildID:           &build.ID,
		TeamID:            &teamID,
		Metadata:          metadata,
		VCpu:              build.Vcpu,
		RamMB:             build.RAMMB,
		MaxInstanceLength: time.Duration(maxInstanceLengthHours) * time.Hour,
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
		if node.sbxInProgress <= 2 && (leastBusyNode == nil || node.CPUUsage < leastBusyNode.CPUUsage) {
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
