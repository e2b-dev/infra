package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func (n *Node) GetSandboxes(ctx context.Context, tracer trace.Tracer) ([]*instance.InstanceInfo, error) {
	childCtx, childSpan := tracer.Start(ctx, "get-sandboxes-from-orchestrator")
	defer childSpan.End()

	client, childCtx := n.GetClient(childCtx)
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
			return nil, fmt.Errorf("failed to parse build ID '%s' for job: %w", config.BuildId, parseErr)
		}

		sandboxesInfo = append(
			sandboxesInfo,
			instance.NewInstanceInfo(
				config.SandboxId,
				config.TemplateId,
				consts.ClientID,
				config.Alias,
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
				n.ID,
				n.ClusterID,
				config.AutoPause,
				config.EnvdAccessToken,
				config.AllowInternetAccess,
				config.BaseTemplateId,
			),
		)
	}

	return sandboxesInfo, nil
}
