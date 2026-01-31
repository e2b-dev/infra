package orchestrator

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gogo/status"
	"github.com/google/uuid"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/api/internal/orchestrator/nodemanager"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type PauseQueueExhaustedError struct{}

func (PauseQueueExhaustedError) Error() string {
	return "The pause queue is exhausted"
}

func (o *Orchestrator) pauseSandbox(ctx context.Context, node *nodemanager.Node, sbx sandbox.Sandbox) error {
	ctx, span := tracer.Start(ctx, "pause-sandbox")
	defer span.End()

	result, err := o.sqlcDB.UpsertSnapshot(ctx, buildUpsertSnapshotParams(sbx, node))
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error inserting snapshot for env", err)

		return err
	}

	err = snapshotInstance(ctx, node, sbx, result.TemplateID, result.BuildID.String())
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
		Status:     string(types.BuildStatusSuccess),
		FinishedAt: &now,
		Reason:     types.BuildReason{},
		BuildID:    result.BuildID,
	})
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error pausing sandbox", err)

		return fmt.Errorf("error pausing sandbox: %w", err)
	}

	return nil
}

func snapshotInstance(ctx context.Context, node *nodemanager.Node, sbx sandbox.Sandbox, templateID, buildID string) error {
	childCtx, childSpan := tracer.Start(ctx, "snapshot-instance")
	defer childSpan.End()

	client, childCtx := node.GetSandboxDeleteCtx(childCtx, sbx.SandboxID, sbx.ExecutionID)
	_, err := client.Sandbox.Pause(
		childCtx, &orchestrator.SandboxPauseRequest{
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

func (o *Orchestrator) WaitForStateChange(ctx context.Context, teamID uuid.UUID, sandboxID string) error {
	return o.sandboxStore.WaitForStateChange(ctx, teamID, sandboxID)
}
