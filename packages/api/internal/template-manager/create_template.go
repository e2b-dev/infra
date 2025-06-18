package template_manager

import (
	"context"
	_ "embed"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

const engineTemplateAlias = "e2b-build-engine"

func (tm *TemplateManager) CreateTemplate(
	t trace.Tracer,
	ctx context.Context,
	teamID uuid.UUID,
	templateID string,
	buildID uuid.UUID,
	kernelVersion,
	firecrackerVersion string,
	startCommand *string,
	vCpuCount,
	diskSizeMB,
	memoryMB int64,
	readyCommand *string,
	fromImage string,
	steps *[]api.TemplateStep,
) error {
	ctx, span := t.Start(ctx, "create-template",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateID),
		),
	)
	defer span.End()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		return fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)
	}

	if !tm.grpc.IsReadyForBuildPlacement() {
		return fmt.Errorf("template manager is not ready for build placement")
	}

	var startCmd string
	if startCommand != nil {
		startCmd = *startCommand
	}

	var readyCmd string
	if readyCommand != nil {
		readyCmd = *readyCommand
	}

	var engineConfig *template_manager.EngineConfig
	env, build, checkErr := tm.templateCache.Get(ctx, engineTemplateAlias, teamID, true)
	if checkErr != nil {
		zap.L().Error("failed to get template from cache", logger.WithTemplateID(templateID), logger.WithBuildID(buildID.String()))
	} else {
		if build.EnvdVersion != nil {
			engineConfig = &template_manager.EngineConfig{
				TemplateId:         env.TemplateID,
				BuildId:            build.ID.String(),
				KernelVersion:      build.KernelVersion,
				FirecrackerVersion: build.FirecrackerVersion,
				// TODO: This might not be needed anymore,
				// needs a follow up in the future PR to remove from the SandboxConfig too.
				HugePages:      features.HasHugePages(),
				EnvdVersion:    *build.EnvdVersion,
				Vcpu:           build.Vcpu,
				RamMb:          build.RamMb,
				BaseTemplateId: env.TemplateID,
			}
		}
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
			StartCommand:       startCmd,
			ReadyCommand:       readyCmd,
			FromImage:          fromImage,
			Steps:              convertTemplateSteps(steps),
		},
		EngineConfig: engineConfig,
	})

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", templateID, err)
	}

	telemetry.ReportEvent(ctx, "Template build started")

	return nil
}

func convertTemplateSteps(steps *[]api.TemplateStep) []*template_manager.TemplateStep {
	if steps == nil {
		return nil
	}

	result := make([]*template_manager.TemplateStep, len(*steps))
	for i, step := range *steps {
		var args []string
		if step.Args != nil {
			args = *step.Args
		}

		result[i] = &template_manager.TemplateStep{
			Type:      step.Type,
			Args:      args,
			Hash:      step.Hash,
			FilesHash: step.FilesHash,
			Force:     step.Force,
		}
	}
	return result
}
