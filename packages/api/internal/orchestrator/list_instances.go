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
	"github.com/e2b-dev/infra/packages/api/internal/utils"
)

func (o *Orchestrator) getInstances(ctx context.Context, nodeID string) ([]*instance.InstanceInfo, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "list-instances")
	defer childSpan.End()

	client, err := o.GetClient(nodeID)
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

		sandboxesInfo = append(sandboxesInfo, &instance.InstanceInfo{
			Instance: api.Sandbox{
				SandboxID:  config.SandboxId,
				TemplateID: config.TemplateId,
				Alias:      config.Alias,
				ClientID:   sbx.ClientId,
			},
			StartTime:         sbx.StartTime.AsTime(),
			EndTime:           sbx.EndTime.AsTime(),
			VCpu:              config.Vcpu,
			RamMB:             config.RamMb,
			BuildID:           buildID,
			TeamID:            teamID,
			Metadata:          config.Metadata,
			MaxInstanceLength: time.Duration(config.MaxSandboxLength) * time.Hour,
		})
	}

	return sandboxesInfo, nil
}

// GetInstances returns all instances for a given node.
func (o *Orchestrator) GetInstances(ctx context.Context, teamID *uuid.UUID) []instance.InstanceInfo {
	_, childSpan := o.tracer.Start(ctx, "get-instances")
	defer childSpan.End()

	return o.instanceCache.GetInstances(teamID)
}
