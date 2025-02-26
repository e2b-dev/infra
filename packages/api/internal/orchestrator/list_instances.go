package orchestrator

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	nNode "github.com/e2b-dev/infra/packages/api/internal/node"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	sbxlogger "github.com/e2b-dev/infra/packages/shared/pkg/logger/sandbox"
	sUtils "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const ServiceName = "orchestration-api"

func (o *Orchestrator) getSandboxes(ctx context.Context, node *nNode.NodeInfo) ([]*instance.InstanceInfo, error) {
	childCtx, childSpan := o.tracer.Start(ctx, "get-sandboxes-from-orchestrator")
	defer childSpan.End()

	client, err := o.GetClient(node.ID)
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

		logger := sbxlogger.NewSandboxLogger(childCtx, sbxlogger.SandboxLoggerConfig{
			ServiceName:      ServiceName,
			IsInternal:       true,
			IsDevelopment:    true,
			SandboxID:        config.SandboxId,
			TemplateID:       config.TemplateId,
			TeamID:           teamID.String(),
			CollectorAddress: os.Getenv("LOGS_COLLECTOR_ADDRESS"),
		})

		sandboxesInfo = append(sandboxesInfo, &instance.InstanceInfo{
			Logger: logger,
			Instance: &api.Sandbox{
				SandboxID:  config.SandboxId,
				TemplateID: config.TemplateId,
				Alias:      config.Alias,
				ClientID:   sbx.ClientId,
			},
			StartTime:          sbx.StartTime.AsTime(),
			EndTime:            sbx.EndTime.AsTime(),
			VCpu:               config.Vcpu,
			RamMB:              config.RamMb,
			BuildID:            &buildID,
			TeamID:             &teamID,
			Metadata:           config.Metadata,
			KernelVersion:      config.KernelVersion,
			FirecrackerVersion: config.FirecrackerVersion,
			EnvdVersion:        config.EnvdVersion,
			TotalDiskSizeMB:    config.TotalDiskSizeMb,
			MaxInstanceLength:  time.Duration(config.MaxSandboxLength) * time.Hour,
			Node:               node,
			AutoPause:          &autoPause,
			Pausing:            sUtils.NewSetOnce[*nNode.NodeInfo](),
		})
	}

	return sandboxesInfo, nil
}

// GetSandboxes returns all instances for a given node.
func (o *Orchestrator) GetSandboxes(ctx context.Context, teamID *uuid.UUID) []instance.InstanceInfo {
	_, childSpan := o.tracer.Start(ctx, "get-sandboxes")
	defer childSpan.End()

	return o.instanceCache.GetInstances(teamID)
}

func (o *Orchestrator) GetInstance(ctx context.Context, id string) (instance.InstanceInfo, error) {
	return o.instanceCache.GetInstance(id)
}
