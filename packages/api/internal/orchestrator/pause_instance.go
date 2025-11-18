package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/gogo/status"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/db/types"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type PauseQueueExhaustedError struct{}

func (PauseQueueExhaustedError) Error() string {
	return "The pause queue is exhausted"
}

func (o *Orchestrator) pauseSandbox(ctx context.Context, node *nodemanager.Node, sbx sandbox.Sandbox) (err error) {
	ctx, span := tracer.Start(ctx, "pause-sandbox")
	defer span.End()

	result, err := o.sqlcDB.UpsertSnapshotEnvAndBuild(ctx, queries.UpsertSnapshotEnvAndBuildParams{
		BaseTemplateID:      sbx.BaseTemplateID,
		TemplateID:          id.Generate(),
		TeamID:              sbx.TeamID,
		SandboxID:           sbx.SandboxID,
		StartedAt:           pgtype.Timestamptz{Time: sbx.StartTime, Valid: true},
		Status:              string(envbuild.StatusSnapshotting),
		OriginNodeID:        node.ID,
		Vcpu:                sbx.VCpu,
		RamMb:               sbx.RamMB,
		TotalDiskSizeMb:     &sbx.TotalDiskSizeMB,
		Metadata:            sbx.Metadata,
		KernelVersion:       sbx.KernelVersion,
		FirecrackerVersion:  sbx.FirecrackerVersion,
		EnvdVersion:         &sbx.EnvdVersion,
		Secure:              sbx.EnvdAccessToken != nil,
		AllowInternetAccess: sbx.AllowInternetAccess,
		AutoPause:           sbx.AutoPause,
		Config: &types.PausedSandboxConfig{
			Version: types.PausedSandboxConfigVersion,
			Network: sbx.Network,
		},
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error inserting snapshot for env", err)

		return err
	}

	err = snapshotInstance(ctx, o, node, sbx, result.TemplateID, result.BuildID.String())
	if errors.Is(err, PauseQueueExhaustedError{}) {
		telemetry.ReportCriticalError(ctx, "pause queue exhausted", err)

		return PauseQueueExhaustedError{}
	}

	if err != nil && !errors.Is(err, PauseQueueExhaustedError{}) {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return fmt.Errorf("error pausing sandbox: %w", err)
	}

	now := time.Now()
	err = o.sqlcDB.UpdateEnvBuildStatus(ctx, queries.UpdateEnvBuildStatusParams{
		Status:     string(envbuild.StatusSuccess),
		FinishedAt: &now,
		Reason:     types.BuildReason{},
		BuildID:    result.BuildID,
		TemplateID: result.TemplateID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return fmt.Errorf("error pausing sandbox: %w", err)
	}

	return nil
}

func snapshotInstance(ctx context.Context, orch *Orchestrator, node *nodemanager.Node, sbx sandbox.Sandbox, templateID, buildID string) error {
	childCtx, childSpan := tracer.Start(ctx, "snapshot-instance")
	defer childSpan.End()

	client, childCtx, err := orch.GetClient(childCtx, sbx.ClusterID, sbx.NodeID)
	if err != nil {
		return fmt.Errorf("failed to get client '%s': %w", sbx.NodeID, err)
	}

	_, err = client.Sandbox.Pause(
		node.GetSandboxDeleteCtx(childCtx, sbx.SandboxID, sbx.ExecutionID),
		&orchestrator.SandboxPauseRequest{
			SandboxId:  sbx.SandboxID,
			TemplateId: templateID,
			BuildId:    buildID,
		},
	)

	if err == nil {
		telemetry.ReportEvent(ctx, "Paused sandbox")

		return nil
	}

	st, ok := status.FromError(err)
	if !ok {
		return err
	}

	if st.Code() == codes.ResourceExhausted {
		return PauseQueueExhaustedError{}
	}

	return fmt.Errorf("failed to pause sandbox '%s': %w", sbx.SandboxID, err)
}

func (o *Orchestrator) WaitForStateChange(ctx context.Context, sandboxID string) error {
	return o.sandboxStore.WaitForStateChange(ctx, sandboxID)
}
