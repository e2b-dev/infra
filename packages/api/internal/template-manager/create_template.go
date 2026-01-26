package template_manager

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	templatecache "github.com/e2b-dev/infra/packages/api/internal/cache/templates"
	"github.com/e2b-dev/infra/packages/api/internal/sandbox"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/db/pkg/types"
	"github.com/e2b-dev/infra/packages/db/queries"
	templatemanagergrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	ut "github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type FromTemplateError struct {
	err     error
	message string
}

func (e *FromTemplateError) Error() string {
	return e.message
}

func (e *FromTemplateError) Unwrap() error {
	return e.err
}

func (tm *TemplateManager) CreateTemplate(
	ctx context.Context,
	teamID uuid.UUID,
	teamSlug string,
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
	clusterID uuid.UUID,
	nodeID string,
	version string,
) (e error) {
	ctx, span := tracer.Start(ctx, "create-template",
		trace.WithAttributes(
			telemetry.WithTemplateID(templateID),
		),
	)
	defer span.End()

	defer func() {
		if e == nil {
			return
		}

		// Report build failure status on any error while creating the template
		telemetry.ReportCriticalError(ctx, "build failed", e, telemetry.WithTemplateID(templateID))
		err := tm.SetStatus(
			ctx,
			buildID,
			types.BuildStatusFailed,
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

	client, err := tm.GetClusterBuildClient(clusterID, nodeID)
	if err != nil {
		return fmt.Errorf("failed to get builder: %w", err)
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
		TeamID:             teamID.String(),
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

	err = setTemplateSource(ctx, tm, teamID, teamSlug, template, fromImage, fromTemplate)
	if err != nil {
		// If the error is related to fromTemplate, set the build status to failed with the appropriate message
		// This is to unify the error handling with fromImage errors
		var fromTemplateErr *FromTemplateError
		if !errors.As(err, &fromTemplateErr) {
			return fmt.Errorf("failed to set template source: %w", err)
		}

		err = tm.SetStatus(
			ctx,
			buildID,
			types.BuildStatusFailed,
			&templatemanagergrpc.TemplateBuildStatusReason{
				Message: err.Error(),
				Step:    ut.ToPtr("base"),
			},
		)
		if err != nil {
			return fmt.Errorf("failed to set build status: %w", err)
		}

		return nil
	}

	_, err = client.Template.TemplateCreate(
		ctx, &templatemanagergrpc.TemplateCreateRequest{
			Template:   template,
			CacheScope: ut.ToPtr(teamID.String()),
			Version:    &version,
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
		buildID,
		types.BuildStatusBuilding,
		nil,
	)
	if err != nil {
		return fmt.Errorf("failed to set build status to building: %w", err)
	}
	telemetry.ReportEvent(ctx, "created new environment", telemetry.WithTemplateID(templateID))

	// Do not wait for global build sync trigger it immediately
	go func(ctx context.Context) {
		ctx, span := tracer.Start(ctx, "template-background-build-env")
		defer span.End()

		l := logger.L().With(logger.WithBuildID(buildID.String()), logger.WithTemplateID(templateID))

		err := tm.BuildStatusSync(ctx, buildID, templateID, clusterID, &nodeID)
		if err != nil {
			l.Error(ctx, "error syncing build status", zap.Error(err))
		}

		telemetry.ReportEvent(ctx, "build status sync completed")

		// Invalidate the cache
		invalidatedKeys := tm.templateCache.InvalidateAllTags(templateID)

		telemetry.ReportEvent(ctx, "invalidated template cache", attribute.StringSlice("invalidated_keys", invalidatedKeys))
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
func setTemplateSource(ctx context.Context, tm *TemplateManager, teamID uuid.UUID, teamSlug string, template *templatemanagergrpc.TemplateConfig, fromImage *string, fromTemplate *string) error {
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
		identifier, tag, err := id.ParseName(*fromTemplate)
		if err != nil {
			return &FromTemplateError{
				err:     err,
				message: fmt.Sprintf("invalid template reference: %s", err),
			}
		}

		// Step 1: Resolve alias to template ID (using cache with fallback for promoted templates)
		aliasInfo, err := tm.templateCache.ResolveAlias(ctx, identifier, teamSlug)
		if err != nil {
			msg := fmt.Sprintf("error resolving base template '%s'", *fromTemplate)
			if errors.Is(err, templatecache.ErrTemplateNotFound) {
				msg = fmt.Sprintf("base template '%s' not found", *fromTemplate)
			}

			return &FromTemplateError{
				err:     err,
				message: msg,
			}
		}

		if !aliasInfo.Public && aliasInfo.TeamID != teamID {
			return &FromTemplateError{
				err:     nil,
				message: fmt.Sprintf("you have no access to use '%s' as a base template", *fromTemplate),
			}
		}

		// Step 2: Get template with build by ID
		baseTemplate, err := tm.sqlcDB.GetTemplateWithBuildByTag(ctx, queries.GetTemplateWithBuildByTagParams{
			TemplateID: aliasInfo.TemplateID,
			Tag:        tag,
		})
		if err != nil {
			return &FromTemplateError{
				err:     err,
				message: fmt.Sprintf("base template '%s' not found", *fromTemplate),
			}
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
