package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/gogo/status"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type ErrPauseQueueExhausted struct{}

func (ErrPauseQueueExhausted) Error() string {
	return "The pause queue is exhausted"
}

func (o *Orchestrator) PauseInstance(
	ctx context.Context,
	sbx *instance.InstanceInfo,
	teamID uuid.UUID,
) error {
	ctx, span := o.tracer.Start(ctx, "pause-sandbox")
	defer span.End()

	snapshotConfig := &db.SnapshotInfo{
		BaseTemplateID:      sbx.TemplateID,
		SandboxID:           sbx.SandboxID,
		SandboxStartedAt:    sbx.StartTime,
		VCPU:                sbx.VCpu,
		RAMMB:               sbx.RamMB,
		TotalDiskSizeMB:     sbx.TotalDiskSizeMB,
		Metadata:            sbx.Metadata(),
		KernelVersion:       sbx.KernelVersion,
		FirecrackerVersion:  sbx.FirecrackerVersion,
		EnvdVersion:         sbx.EnvdVersion,
		EnvdSecured:         sbx.EnvdAccessToken != nil,
		AllowInternetAccess: sbx.AllowInternetAccess,
	}

	envBuild, err := o.dbClient.NewSnapshotBuild(
		ctx,
		snapshotConfig,
		teamID,
		sbx.NodeID,
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return err
	}

	err = snapshotInstance(ctx, o, sbx, *envBuild.EnvID, envBuild.ID.String())
	if errors.Is(err, ErrPauseQueueExhausted{}) {
		telemetry.ReportCriticalError(ctx, "pause queue exhausted", err)

		return ErrPauseQueueExhausted{}
	}

	if err != nil && !errors.Is(err, ErrPauseQueueExhausted{}) {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return fmt.Errorf("error pausing sandbox: %w", err)
	}

	err = o.dbClient.EnvBuildSetStatus(ctx, *envBuild.EnvID, envBuild.ID, envbuild.StatusSuccess, nil)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return fmt.Errorf("error pausing sandbox: %w", err)
	}

	return nil
}

func snapshotInstance(ctx context.Context, orch *Orchestrator, sbx *instance.InstanceInfo, templateID, buildID string) error {
	childCtx, childSpan := orch.tracer.Start(ctx, "snapshot-instance")
	defer childSpan.End()

	node := orch.GetNode(sbx.ClusterID, sbx.NodeID)
	if node == nil {
		return fmt.Errorf("failed to get node '%s'", sbx.NodeID)
	}

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
		return ErrPauseQueueExhausted{}
	}

	return fmt.Errorf("failed to pause sandbox '%s': %w", sbx.SandboxID, err)
}
