package orchestrator

import (
	"context"
	"fmt"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (o *Orchestrator) PauseInstance(ctx context.Context, sbx *instance.InstanceInfo) error {
	_, childSpan := o.tracer.Start(ctx, "delete-instance")
	defer childSpan.End()

	client, err := o.GetClient(sbx.Instance.ClientID)
	if err != nil {
		return fmt.Errorf("failed to get client '%s': %w", sbx.Instance.ClientID, err)
	}

	defer o.DeleteInstance(ctx, sbx.Instance.SandboxID)

	// TODO: Create new build and template + snapshot if there is no snapshot for this sandbox_id already

	_, err = client.Sandbox.Pause(ctx, &orchestrator.SandboxPauseRequest{
		SandboxId:  sbx.Instance.SandboxID,
		TemplateId: // New snapshot tempalte
		BuildId:    // New snapshot build
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to pause sandbox '%s': %w", sbx.Instance.SandboxID, err)
	}

	telemetry.ReportEvent(ctx, "Paused sandbox")

	return nil
}
