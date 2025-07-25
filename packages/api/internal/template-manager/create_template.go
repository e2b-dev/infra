package template_manager

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/models/envbuild"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

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
	fromImage *string,
	fromTemplate *string,
	force *bool,
	steps *[]api.TemplateStep,
	clusterID *uuid.UUID,
	clusterNodeID *string,
) (e error) {
	ctx, span := t.Start(ctx, "create-template",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateID),
		),
	)
	defer span.End()

	defer func() {
		if e == nil {
			return
		}

		// Report build failur status on any error while creating the template
		telemetry.ReportCriticalError(ctx, "build failed", e, telemetry.WithTemplateID(templateID))
		msg := fmt.Sprintf("error when building env: %s", e)
		err := tm.SetStatus(
			ctx,
			templateID,
			buildID,
			envbuild.StatusFailed,
			&msg,
		)
		if err != nil {
			e = errors.Join(e, fmt.Errorf("failed to set build status to failed: %w", err))
		}
	}()

	features, err := sandbox.NewVersionInfo(firecrackerVersion)
	if err != nil {
		return fmt.Errorf("failed to get features for firecracker version '%s': %w", firecrackerVersion, err)
	}

	cli, err := tm.GetBuildClient(clusterID, clusterNodeID, true)
	if err != nil {
		return fmt.Errorf("failed to get builder edgeHttpClient: %w", err)
	}

	var startCmd string
	if startCommand != nil {
		startCmd = *startCommand
	}
	var readyCmd string
	if readyCommand != nil {
		readyCmd = *readyCommand
	}

	template := &templatemanagergrpc.TemplateConfig{
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
		Force:              force,
		Steps:              convertTemplateSteps(steps),
	}

	// Set the source (either fromImage or fromTemplate)
	if fromTemplate != nil && *fromTemplate != "" {
		// Look up the base template by alias to get its metadata
		baseTemplate, err := tm.sqlcDB.GetEnvWithBuild(ctx, *fromTemplate)
		if err != nil {
			return fmt.Errorf("failed to find base template '%s': %w", *fromTemplate, err)
		}

		startCmd := ""
		if baseTemplate.EnvBuild.StartCmd != nil {
			startCmd = *baseTemplate.EnvBuild.StartCmd
		}

		readyCmd := ""
		if baseTemplate.EnvBuild.ReadyCmd != nil {
			readyCmd = *baseTemplate.EnvBuild.ReadyCmd
		}

		template.Source = &templatemanagergrpc.TemplateConfig_FromTemplate{
			FromTemplate: &templatemanagergrpc.FromTemplateConfig{
				Alias:              *fromTemplate,
				TemplateID:         baseTemplate.Env.ID,
				BuildID:            baseTemplate.EnvBuild.ID.String(),
				KernelVersion:      baseTemplate.EnvBuild.KernelVersion,
				FirecrackerVersion: baseTemplate.EnvBuild.FirecrackerVersion,
				StartCommand:       startCmd,
				ReadyCommand:       readyCmd,
			},
		}
	} else if fromImage != nil {
		template.Source = &templatemanagergrpc.TemplateConfig_FromImage{
			FromImage: *fromImage,
		}
	} else {
		return fmt.Errorf("either fromImage or fromTemplate must be provided")
	}

	reqCtx := metadata.NewOutgoingContext(ctx, cli.GRPC.Metadata)
	_, err = cli.GRPC.Client.Template.TemplateCreate(
		reqCtx, &templatemanagergrpc.TemplateCreateRequest{
			Template:   template,
			CacheScope: ut.ToPtr(teamID.String()),
		},
	)

	err = utils.UnwrapGRPCError(err)
	if err != nil {
		return fmt.Errorf("failed to create template '%s': %w", templateID, err)
	}
	telemetry.ReportEvent(ctx, "Template build started")

	// status building must be set after build is triggered because then
	// it's possible build status job will be triggered before build cache on template manager is created and build will fail
	err = tm.SetStatus(
		ctx,
		templateID,
		buildID,
		envbuild.StatusBuilding,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to set build status to building: %w", err)
	}
	telemetry.ReportEvent(ctx, "created new environment", telemetry.WithTemplateID(templateID))

	// Do not wait for global build sync trigger it immediately
	go func(ctx context.Context) {
		buildContext, buildSpan := t.Start(ctx, "template-background-build-env")
		defer buildSpan.End()

		err := tm.BuildStatusSync(buildContext, buildID, templateID, clusterID, clusterNodeID)
		if err != nil {
			zap.L().Error("error syncing build status", zap.Error(err))
		}

		// Invalidate the cache
		tm.templateCache.Invalidate(templateID)
	}(context.WithoutCancel(ctx))

	return nil
}

func convertTemplateSteps(steps *[]api.TemplateStep) []*templatemanagergrpc.TemplateStep {
	if steps == nil {
		return nil
	}

	result := make([]*templatemanagergrpc.TemplateStep, len(*steps))
	for i, step := range *steps {
		var args []string
		if step.Args != nil {
			args = *step.Args
		}

		result[i] = &templatemanagergrpc.TemplateStep{
			Type:      step.Type,
			Args:      args,
			FilesHash: step.FilesHash,
			Force:     step.Force,
		}
	}
	return result
}
