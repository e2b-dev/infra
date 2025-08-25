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
	fromImageRegistry *api.FromImageRegistry,
	force *bool,
	steps *[]api.TemplateStep,
	clusterID *uuid.UUID,
	nodeID string,
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

	cli, err := tm.GetBuildClient(clusterID, nodeID, true)
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

	imageRegistry, err := convertImageRegistry(fromImageRegistry)
	if err != nil {
		return fmt.Errorf("failed to convert image registry: %w", err)
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
		FromImageRegistry:  imageRegistry,
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

		err := tm.BuildStatusSync(buildContext, buildID, templateID, clusterID, nodeID)
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

func convertImageRegistry(registry *api.FromImageRegistry) (*templatemanagergrpc.FromImageRegistry, error) {
	if registry == nil {
		return nil, nil
	}

	// The OpenAPI FromImageRegistry is a union type, so we need to check the discriminator
	discriminator, err := registry.Discriminator()
	if err != nil {
		return nil, err
	}

	switch discriminator {
	case "aws":
		awsReg, err := registry.AsAWSRegistry()
		if err != nil {
			return nil, err
		}
		return &templatemanagergrpc.FromImageRegistry{
			Type: &templatemanagergrpc.FromImageRegistry_Aws{
				Aws: &templatemanagergrpc.AWSRegistry{
					AwsAccessKeyId:     awsReg.AwsAccessKeyId,
					AwsSecretAccessKey: awsReg.AwsSecretAccessKey,
					AwsRegion:          awsReg.AwsRegion,
				},
			},
		}, nil
	case "gcp":
		gcpReg, err := registry.AsGCPRegistry()
		if err != nil {
			return nil, err
		}
		return &templatemanagergrpc.FromImageRegistry{
			Type: &templatemanagergrpc.FromImageRegistry_Gcp{
				Gcp: &templatemanagergrpc.GCPRegistry{
					ServiceAccountJson: gcpReg.ServiceAccountJson,
				},
			},
		}, nil
	case "registry":
		generalReg, err := registry.AsGeneralRegistry()
		if err != nil {
			return nil, err
		}
		return &templatemanagergrpc.FromImageRegistry{
			Type: &templatemanagergrpc.FromImageRegistry_General{
				General: &templatemanagergrpc.GeneralRegistry{
					Username: generalReg.Username,
					Password: generalReg.Password,
				},
			},
		}, nil
	default:
		return nil, fmt.Errorf("unknown registry type: %s", discriminator)
	}
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
