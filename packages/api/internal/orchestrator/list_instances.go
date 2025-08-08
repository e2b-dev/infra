package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	nNode "github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func (o *Orchestrator) getSandboxes(ctx context.Context, node *nNode.NodeInfo) ([]*instance.InstanceInfo, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "get-sandboxes-from-orchestrator")
	defer childSpan.End()

	client, childCtx, err := o.GetClient(childCtx, node.ClusterID, node.NodeID)
	if err != nil {
		return nil, fmt.Errorf("failed to get GRPC client: %w", err)
	}

	res, err := client.Sandbox.List(childCtx, &empty.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	sandboxes := res.GetSandboxes()

	sandboxesInfo := make([]*instance.InstanceInfo, 0, len(sandboxes))

	for _, sbx := range sandboxes {
		config := sbx.GetConfig()

		if config == nil {
			return nil, fmt.Errorf("sandbox config is nil when listing sandboxes: %#v", sbx)
		}

		teamID, parseErr := uuid.Parse(config.TeamId)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse team ID '%s' for job: %w", config.TeamId, parseErr)
		}

		buildID, parseErr := uuid.Parse(config.BuildId)
		if parseErr != nil {
			return nil, fmt.Errorf("failed to parse build ID '%s' for job: %w", config.BuildId, err)
		}

		autoPause := instance.InstanceAutoPauseDefault
		if config.AutoPause != nil {
			autoPause = *config.AutoPause
		}

		sandboxesInfo = append(
			sandboxesInfo,
			instance.NewInstanceInfo(
				&api.Sandbox{
					SandboxID:  config.SandboxId,
					TemplateID: config.TemplateId,
					Alias:      config.Alias,
					ClientID:   consts.ClientID,
				},
				config.ExecutionId,
				teamID,
				buildID,
				config.Metadata,
				time.Duration(config.MaxSandboxLength)*time.Hour,
				sbx.StartTime.AsTime(),
				sbx.EndTime.AsTime(),
				config.Vcpu,
				config.TotalDiskSizeMb,
				config.RamMb,
				config.KernelVersion,
				config.FirecrackerVersion,
				config.EnvdVersion,
				node,
				autoPause,
				config.EnvdAccessToken,
				config.AllowInternetAccess,
				config.BaseTemplateId,
			),
		)
	}

	return sandboxesInfo, nil
}

// GetSandboxes returns all instances for a given node.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID *uuid.UUID) []*instance.InstanceInfo {
	_, childSpan := o.tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.instanceCache.GetInstances(teamID)
}

func (o *Orchestrator) GetInstance(_ context.Context, id string) (*instance.InstanceInfo, error) {
	return o.instanceCache.Get(id)
}
