package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/gogo/status"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type PauseQueueExhaustedError struct{}

func (PauseQueueExhaustedError) Error() string {
	return "The pause queue is exhausted"
}

func (o *Orchestrator) pauseSandbox(ctx context.Context, node *nodemanager.Node, sbx *instance.InstanceInfo) (err error) {
	ctx, span := o.tracer.Start(ctx, "pause-sandbox")
	defer span.End()

	snapshotConfig := &db.SnapshotInfo{
		BaseTemplateID:      sbx.BaseTemplateID,
		SandboxID:           sbx.SandboxID,
		SandboxStartedAt:    sbx.StartTime,
		VCPU:                sbx.VCpu,
		RAMMB:               sbx.RamMB,
		TotalDiskSizeMB:     sbx.TotalDiskSizeMB,
		Metadata:            sbx.Metadata,
		KernelVersion:       sbx.KernelVersion,
		FirecrackerVersion:  sbx.FirecrackerVersion,
		EnvdVersion:         sbx.EnvdVersion,
		EnvdSecured:         sbx.EnvdAccessToken != nil,
		AllowInternetAccess: sbx.AllowInternetAccess,
		AutoPause:           sbx.AutoPause,
	}

	envBuild, err := o.dbClient.NewSnapshotBuild(
		ctx,
		snapshotConfig,
		sbx.TeamID,
		sbx.NodeID,
	)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return err
	}

	err = snapshotInstance(ctx, o, node, sbx, *envBuild.EnvID, envBuild.ID.String())
	if errors.Is(err, PauseQueueExhaustedError{}) {
		telemetry.ReportCriticalError(ctx, "pause queue exhausted", err)

		return PauseQueueExhaustedError{}
	}

	if err != nil && !errors.Is(err, PauseQueueExhaustedError{}) {
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

func snapshotInstance(ctx context.Context, orch *Orchestrator, node *nodemanager.Node, sbx *instance.InstanceInfo, templateID, buildID string) error {
	childCtx, childSpan := orch.tracer.Start(ctx, "snapshot-instance")
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
