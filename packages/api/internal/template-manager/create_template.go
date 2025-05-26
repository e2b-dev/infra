package template_manager

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (tm *TemplateManager) CreateTemplate(
	t trace.Tracer,
	ctx context.Context,
	templateID string,
	buildID uuid.UUID,
	kernelVersion,
	firecrackerVersion,
	startCommand string,
	vCpuCount,
	diskSizeMB,
	memoryMB int64,
) error {
	ctx, span := t.Start(ctx, "create-template",
		trace.WithAttributes(
			attribute.String("env.id", templateID),
		),
	)
	defer span.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)

		return errMsg
	}

	if !tm.grpc.IsReadyForBuildPlacement() {
		return fmt.Errorf("template manager is not ready for build placement")
	}

	_, err = tm.grpc.TemplateClient.TemplateCreate(ctx, &template_manager.TemplateCreateRequest{
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
