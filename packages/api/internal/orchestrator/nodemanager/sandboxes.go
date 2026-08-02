package nodemanager

import (
	"cmp"
	"context"
	"fmt"

	"github.com/golang/protobuf/ptypes/empty"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager")

// GetOrphanCandidates lists the sandboxes the node reports as running.
func (n *Node) GetOrphanCandidates(ctx context.Context) ([]sandbox.Sandbox, error) {
	childCtx, childSpan := tracer.Start(ctx, "get-sandboxes-from-orchestrator")
	defer childSpan.End()

	client, childCtx := n.GetClient(childCtx)
	res, err := client.Sandbox.List(childCtx, &empty.Empty{})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return nil, fmt.Errorf("failed to list sandboxes: %w", err)
	}

	sandboxes := res.GetSandboxes()

	sandboxesInfo := make([]sandbox.Sandbox, 0, len(sandboxes))

	for _, sbx := range sandboxes {
		// config is deprecated and only read as a fallback for orchestrators
		// that predate the scalar fields. Proto getters are nil-safe.
		config := sbx.GetConfig() //nolint:staticcheck // rollout fallback

		sandboxID := cmp.Or(sbx.GetSandboxId(), config.GetSandboxId())
		rawTeamID := cmp.Or(sbx.GetTeamId(), config.GetTeamId())

		teamID, parseErr := uuid.Parse(rawTeamID)
		if parseErr != nil {
			logger.L().Error(childCtx, "Skipping sandbox with unparseable team ID during node sync",
				zap.Error(parseErr),
				zap.String("team_id", rawTeamID),
				logger.WithSandboxID(sandboxID),
				logger.WithNodeID(n.ID),
			)

			continue
		}

		sandboxesInfo = append(sandboxesInfo, sandbox.Sandbox{
			SandboxID:   sandboxID,
			TeamID:      teamID,
			ExecutionID: cmp.Or(sbx.GetExecutionId(), config.GetExecutionId()),
			VCpu:        cmp.Or(sbx.GetVcpu(), config.GetVcpu()),
			RamMB:       cmp.Or(sbx.GetRamMb(), config.GetRamMb()),
			StartTime:   sbx.GetStartTime().AsTime(),
			NodeID:      n.ID,
			ClusterID:   n.ClusterID,
		})
	}

	return sandboxesInfo, nil
}

func ConvertOrchestratorMountsToDatabaseMounts(mounts []*orchestrator.SandboxVolumeMount) []*types.SandboxVolumeMountConfig {
	var results []*types.SandboxVolumeMountConfig

	for _, item := range mounts {
		results = append(results, &types.SandboxVolumeMountConfig{
			ID:   item.GetId(),
			Type: item.GetType(),
			Name: item.GetName(),
			Path: item.GetPath(),
		})
	}

	return results
}
