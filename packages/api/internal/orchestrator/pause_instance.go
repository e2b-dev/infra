package orchestrator

import (
	"context"
	"errors"
	"fmt"

	"github.com/gogo/status"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
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
	tracer trace.Tracer,
	sbx *instance.InstanceInfo,
	teamID uuid.UUID,
) error {
	ctx, span := o.tracer.Start(ctx, "pause-sandbox")
	defer span.End()

	snapshotConfig := &db.SnapshotInfo{
		BaseTemplateID:     sbx.Instance.TemplateID,
		SandboxID:          sbx.Instance.SandboxID,
		SandboxStartedAt:   sbx.StartTime,
		VCPU:               sbx.VCpu,
		RAMMB:              sbx.RamMB,
		TotalDiskSizeMB:    sbx.TotalDiskSizeMB,
		Metadata:           sbx.Metadata,
		KernelVersion:      sbx.KernelVersion,
		FirecrackerVersion: sbx.FirecrackerVersion,
		EnvdVersion:        sbx.Instance.EnvdVersion,
	}

	envBuild, err := o.dbClient.NewSnapshotBuild(
		ctx,
		snapshotConfig,
		teamID,
	)
	if err != nil {
		errMsg := fmt.Errorf("error pausing sandbox: %w", err)

		telemetry.ReportCriticalError(ctx, errMsg)

		return errMsg
	}

	err = snapshotInstance(ctx, o, sbx, *envBuild.EnvID, envBuild.ID.String())
	if errors.Is(err, ErrPauseQueueExhausted{}) {
		telemetry.ReportCriticalError(ctx, fmt.Errorf("pause queue exhausted %w", err))

		return ErrPauseQueueExhausted{}
	}

	if err != nil && !errors.Is(err, ErrPauseQueueExhausted{}) {
		errMsg := fmt.Errorf("error pausing sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return errMsg
	}

	err = o.dbClient.EnvBuildSetStatus(ctx, *envBuild.EnvID, envBuild.ID, envbuild.StatusSuccess)
	if err != nil {
		errMsg := fmt.Errorf("error pausing sandbox: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return errMsg
	}

	return nil
}

func snapshotInstance(ctx context.Context, orch *Orchestrator, sbx *instance.InstanceInfo, templateID, buildID string) error {
	_, childSpan := orch.tracer.Start(ctx, "snapshot-instance")
	defer childSpan.End()

	client, err := orch.GetClient(sbx.Instance.ClientID)
	if err != nil {
		return fmt.Errorf("failed to get client '%s': %w", sbx.Instance.ClientID, err)
	}

	_, err = client.Sandbox.Pause(ctx, &orchestrator.SandboxPauseRequest{
		SandboxId:  sbx.Instance.SandboxID,
		TemplateId: templateID,
		BuildId:    buildID,
	})

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

	return fmt.Errorf("failed to pause sandbox '%s': %w", sbx.Instance.SandboxID, err)
}
