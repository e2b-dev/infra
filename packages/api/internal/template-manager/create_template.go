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
	vCpuCount,
	diskSizeMB,
	memoryMB int64,
	fromImage *string,
	fromTemplate *string,
	force *bool,
	steps *[]api.TemplateStep,
	clusterID *uuid.UUID,
	clusterNodeID *string,
	startCommand *api.CommandConfig,
	readyCommand *api.CommandConfig,
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
		err := tm.SetStatus(
			ctx,
			templateID,
			buildID,
			envbuild.StatusFailed,
			&templatemanagergrpc.TemplateBuildStatusReason{
				Message: fmt.Sprintf("error when building env: %s", e),
			},
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

	var startCmd *templatemanagergrpc.CommandConfig
	if startCommand != nil {
		startCmd = &templatemanagergrpc.CommandConfig{
			Cmd:  startCommand.Cmd,
			User: startCommand.User,
		}
	}

	var readyCmd *templatemanagergrpc.CommandConfig
	if readyCommand != nil {
		readyCmd = &templatemanagergrpc.CommandConfig{
			Cmd:  readyCommand.Cmd,
			User: readyCommand.User,
		}
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
		Force:              force,
		Steps:              convertTemplateSteps(steps),
		StartCommand:       startCmd,
		ReadyCommand:       readyCmd,
	}

	err = setTemplateSource(ctx, tm, teamID, template, fromImage, fromTemplate)
	if err != nil {
		return fmt.Errorf("failed to set template source: %w", err)
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

// setTemplateSource sets the source (either fromImage or fromTemplate)
func setTemplateSource(ctx context.Context, tm *TemplateManager, teamID uuid.UUID, template *templatemanagergrpc.TemplateConfig, fromImage *string, fromTemplate *string) error {
	// hasImage can be empty for v1 template builds
	hasImage := fromImage != nil
	hasTemplate := fromTemplate != nil && *fromTemplate != ""

	// Validate input: exactly one source must be provided
	switch {
	case hasImage && hasTemplate:
		return fmt.Errorf("cannot specify both fromImage and fromTemplate")
	case !hasImage && !hasTemplate:
		return fmt.Errorf("must specify either fromImage or fromTemplate")
	case hasTemplate:
		// Look up the base template by alias to get its metadata
		baseTemplate, err := tm.sqlcDB.GetTemplateWithBuild(ctx, *fromTemplate)
		if err != nil {
			return fmt.Errorf("failed to find base template '%s': %w", *fromTemplate, err)
		}

		if !baseTemplate.Env.Public && baseTemplate.Env.TeamID != teamID {
			return fmt.Errorf("you have no access to use '%s' as a base template", *fromTemplate)
		}

		template.Source = &templatemanagergrpc.TemplateConfig_FromTemplate{
			FromTemplate: &templatemanagergrpc.FromTemplateConfig{
				Alias:   *fromTemplate,
				BuildID: baseTemplate.EnvBuild.ID.String(),
			},
		}
	default: // hasImage
		template.Source = &templatemanagergrpc.TemplateConfig_FromImage{
			FromImage: *fromImage,
		}
	}
	return nil
}
