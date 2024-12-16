package orchestrator

import (
	"context"
	"fmt"

	"github.com/gogo/status"
	"google.golang.org/grpc/codes"

	"github.com/e2b-dev/infra/packages/api/internal/cache/instance"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type ErrPauseQueueExhausted struct{}

func (ErrPauseQueueExhausted) Error() string {
	return "The pause queue is exhausted"
}

func (o *Orchestrator) PauseInstance(ctx context.Context, sbx *instance.InstanceInfo, templateID, buildID string) error {
	_, childSpan := o.tracer.Start(ctx, "pause-instance")
	defer childSpan.End()

	client, err := o.GetClient(sbx.Instance.ClientID)
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
