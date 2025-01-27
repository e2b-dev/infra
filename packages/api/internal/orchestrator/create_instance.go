package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"log"
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
	clientID *string,
	baseTemplateID string,
	autoPause bool,
) (*api.Sandbox, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "create-sandbox")
	defer childSpan.End()

	// // Check if team has reached max instances
	// err, releaseTeamSandboxReservation := o.instanceCache.Reserve(sandboxID, teamID, maxInstancesPerTeam)
	// if err != nil {
	// 	errMsg := fmt.Errorf("team '%s' has reached the maximum number of instances (%d)", teamID, maxInstancesPerTeam)
	// 	telemetry.ReportCriticalError(ctx, fmt.Errorf("%w (error: %w)", errMsg, err))
	//
	// 	return nil, fmt.Errorf(
	// 		"you have reached the maximum number of concurrent E2B sandboxes (%d). If you need more, "+
	// 			"please contact us at 'https://e2b.dev/docs/getting-help'", maxInstancesPerTeam)
	// }
	//
	// telemetry.ReportEvent(childCtx, "Reserved sandbox for team")
	// defer releaseTeamSandboxReservation()

	features, err := sandbox.NewVersionInfo(build.FirecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", build.FirecrackerVersion, err)

		return nil, errMsg
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

	for {
		if node == nil {
			node, err = o.getLeastBusyNode(childCtx)
			if err != nil {
				errMsg := fmt.Errorf("failed to get least busy node: %w", err)
				telemetry.ReportError(childCtx, errMsg)

				return nil, errMsg
			}
		}

		// To creating a lot of sandboxes at once on the same node
		node.sbxsInProgress.Insert(sandboxID, &sbxInProgress{
			MiBMemory: build.RAMMB,
			CPUs:      build.Vcpu,
		})

		_, err = node.Client.Sandbox.Create(ctx, sbxRequest)
		// The request is done, we will either add it to the cache or remove it from the node

		if err == nil {
			// The sandbox was created successfully
			break
		}

		err = utils.UnwrapGRPCError(err)
		if err != nil {
			node.sbxsInProgress.Remove(sandboxID)
			if node.Client.connection.GetState() != connectivity.Ready {
				// If the connection is not ready, we should remove the node from the list
				o.nodes.Remove(node.Info.ID)
			} else {
				log.Printf("failed to create sandbox on node '%s': %v", node.Info.ID, err)

				return nil, fmt.Errorf("failed to create a new sandbox, if the problem persists, contact us")
			}
		}

		// The node is not available, try again with another node
		node = nil
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
		Node:               node.Info,
		AutoPause:          autoPause,
	}

	cacheErr := o.instanceCache.Add(instanceInfo, true)
	if cacheErr != nil {
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

func (o *Orchestrator) getLeastBusyNode(ctx context.Context) (leastBusyNode *Node, err error) {
	childCtx, childSpan := o.tracer.Start(ctx, "get-least-busy-node")
	defer childSpan.End()

	for {
		if childCtx.Err() != nil {
			return nil, fmt.Errorf("context was canceled")
		}

		// TODO: Incorporate the node's cached builds and total resources into the decision
		for _, node := range o.nodes.Items() {
			// To prevent overloading the node
			if len(node.sbxsInProgress.Items()) > 3 || node.Status() != api.NodeStatusReady {
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

		// If no node is available, wait for a bit
		time.Sleep(10 * time.Millisecond)
	}
}
