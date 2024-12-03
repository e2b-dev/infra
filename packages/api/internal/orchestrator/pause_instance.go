package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) PauseInstance(ctx context.Context, sbx *instance.InstanceInfo, templateID, buildID string) error {
	_, childSpan := o.tracer.Start(ctx, "pause-instance")
	defer childSpan.End()

	client, err := o.GetClient(sbx.Instance.ClientID)
	if err != nil {
		return fmt.Errorf("failed to get client '%s': %w", sbx.Instance.ClientID, err)
	}

	defer o.DeleteInstance(ctx, sbx.Instance.SandboxID)

	_, err = client.Sandbox.Pause(ctx, &orchestrator.SandboxPauseRequest{
		SandboxId:  sbx.Instance.SandboxID,
		TemplateId: templateID,
		BuildId:    buildID,
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to pause sandbox '%s': %w", sbx.Instance.SandboxID, err)
	}

	telemetry.ReportEvent(ctx, "Paused sandbox")

	return nil
}
