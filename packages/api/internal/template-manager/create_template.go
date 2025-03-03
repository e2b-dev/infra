package template_manager

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"strconv"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/cache/builds"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/db"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (tm *TemplateManager) CreateTemplate(
	t trace.Tracer,
	ctx context.Context,
	db *db.DB,
	buildCache *builds.BuildCache,
	templateID string,
	buildID uuid.UUID,
	kernelVersion,
	firecrackerVersion,
	startCommand string,
	vCpuCount,
	diskSizeMB,
	memoryMB int64,
) error {
	childCtx, childSpan := t.Start(ctx, "create-template",
		trace.WithAttributes(
			attribute.String("env.id", templateID),
		),
	)
	defer childSpan.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		errMsg := fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)

		return errMsg
	}

	telemetry.ReportEvent(childCtx, "Got FC version info")

	logs, err := tm.grpc.Client.TemplateCreate(ctx, &template_manager.TemplateCreateRequest{
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

	// Wait for the build to finish and save logs
	for {
		_, receiveErr := logs.Recv()
		if receiveErr == io.EOF {
			break
		} else if receiveErr != nil {
			// There was an error during the build
			return fmt.Errorf("error when building env: %w", receiveErr)
		}
	}

	trailer := logs.Trailer()
	rootfsSizeStr, ok := trailer[storage.RootfsSizeKey]
	if !ok {
		return fmt.Errorf("rootfs size not found in trailer")
	}

	diskSize, parseErr := strconv.ParseInt(rootfsSizeStr[0], 10, 64)
	if parseErr != nil {
		return fmt.Errorf("error when parsing rootfs size: %w", parseErr)
	}

	envdVersion, ok := trailer[storage.EnvdVersionKey]
	if !ok {
		return fmt.Errorf("envd version not found in trailer")
	}

	err = db.FinishEnvBuild(childCtx, templateID, buildID, diskSize, envdVersion[0])
	if err != nil {
		return fmt.Errorf("error when finishing build: %w", err)
	}

	telemetry.ReportEvent(childCtx, "created new environment", attribute.String("env.id", templateID))

	cacheErr := buildCache.SetDone(templateID, buildID, api.TemplateBuildStatusReady)
	if cacheErr != nil {
		err = fmt.Errorf("error when setting build done in logs: %w", cacheErr)
		telemetry.ReportCriticalError(childCtx, cacheErr)
	}

	telemetry.ReportEvent(childCtx, "Template build started")

	return nil
}
