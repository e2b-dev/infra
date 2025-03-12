package template_manager

import (
	"context"
	_ "embed"
	"fmt"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

func (tm *TemplateManager) CreateTemplate(
	t trace.Tracer,
	span trace.SpanContext,
	templateID string,
	buildID uuid.UUID,
	kernelVersion,
	firecrackerVersion,
	startCommand string,
	vCpuCount,
	diskSizeMB,
	memoryMB int64,
) error {
	ctx, ctxSpan := t.Start(
		trace.ContextWithSpanContext(context.Background(), span),
		"background-build-env",
		trace.WithAttributes(
			attribute.String("env.id", templateID),
		),
	)
	defer ctxSpan.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)

		return errMsg
	}

	_, err = tm.grpc.Client.TemplateCreate(ctx, &template_manager.TemplateCreateRequest{
		Template: &template_manager.TemplateConfig{
			TemplateID:         templateID,
			BuildID:            buildID.String(),
			VCpuCount:          int32(vCpuCount),
			MemoryMB:           int32(memoryMB),
			DiskSizeMB:         int32(diskSizeMB),
			KernelVersion:      kernelVersion,
			FirecrackerVersion: firecrackerVersion,
			HugePages:          features.HasHugePages(),
			StartCommand:       startCommand,
		},
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", templateID, err)
	}

	telemetry.ReportEvent(ctx, "Template build started")

	return nil
}
