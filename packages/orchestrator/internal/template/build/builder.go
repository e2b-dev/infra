package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/commands"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/base"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/finalize"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/steps"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/user"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	buildcache "github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/shared/pkg/templates"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

const progressDelay = 5 * time.Second

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build")

type Builder struct {
	logger logger.Logger

	config              cfg.BuilderConfig
	sandboxFactory      *sandbox.Factory
	templateStorage     storage.StorageProvider
	buildStorage        storage.StorageProvider
	artifactRegistry    artifactsregistry.ArtifactsRegistry
	dockerhubRepository dockerhub.RemoteRepository
	proxy               *proxy.SandboxProxy
	sandboxes           *sandbox.Map
	templateCache       *sbxtemplate.Cache
	metrics             *metrics.BuildMetrics
	featureFlags        *featureflags.Client
}

func NewBuilder(
	config cfg.BuilderConfig,
	logger logger.Logger,
	featureFlags *featureflags.Client,
	sandboxFactory *sandbox.Factory,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	dockerhubRepository dockerhub.RemoteRepository,
	proxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	templateCache *sbxtemplate.Cache,
	buildMetrics *metrics.BuildMetrics,
) *Builder {
	return &Builder{
		config:              config,
		logger:              logger,
		featureFlags:        featureFlags,
		sandboxFactory:      sandboxFactory,
		templateStorage:     templateStorage,
		buildStorage:        buildStorage,
		artifactRegistry:    artifactRegistry,
		dockerhubRepository: dockerhubRepository,
		proxy:               proxy,
		sandboxes:           sandboxes,
		templateCache:       templateCache,
		metrics:             buildMetrics,
	}
}

type Result struct {
	EnvdVersion  string
	RootfsSizeMB int64
}

// Build builds the template, uploads it to storage and returns the result metadata.
// It works the following:
// 1. Get docker image from the remote repository
// 2. Inject new file layers with the required setup for hostname, dns, envd service configuration, basic provisioning script that is run before most of VM services
// 3. Extract ext4 filesystem
// 4. Start FC VM with BusyBox init that runs just the provisioning script, wait for exit. This will install systemd, that is later used for proper VM boot.
// 5. Start the FC VM (using systemd) and wait for Envd
// 6. Build the template steps/layers
// 7. Restart the sandbox and run two additional commands:
//   - configuration script (enable swap, create user, change folder permissions, etc.)
//   - start command (if defined), together with the ready command (always with default value if not defined)
//
// 8. Snapshot
// 9. Upload template (and all not yet uploaded layers)
func (b *Builder) Build(ctx context.Context, template storage.TemplateFiles, cfg config.TemplateConfig, logsCore zapcore.Core) (r *Result, e error) {
	ctx, childSpan := tracer.Start(ctx, "build")
	defer childSpan.End()

	// setup launch darkly context
	ctx = featureflags.SetContext(
		ctx,
		featureflags.TemplateContext(cfg.TemplateID),
		featureflags.TeamContext(cfg.TeamID),
	)

	// Record build duration and result at the end
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		success := e == nil && r != nil
		b.metrics.RecordBuildDuration(ctx, duration, success)

		if success {
			b.metrics.RecordBuildResult(ctx, cfg.TeamID, true)
			b.metrics.RecordRootfsSize(ctx, r.RootfsSizeMB<<constants.ToMBShift)
		} else if !errors.Is(e, context.Canceled) {
			// Skip reporting failure metrics only on explicit cancellation
			b.metrics.RecordBuildResult(ctx, cfg.TeamID, false)
		}
	}()

	cacheScope := cfg.CacheScope

	// Validate template, update force layers if needed
	cfg = forceSteps(cfg)

	isV1Build := utils.IsVersion(cfg.Version, templates.TemplateV1Version) || (cfg.FromImage == "" && cfg.FromTemplate == nil)

	l := logger.NewTracedLoggerFromCore(logsCore)
	defer func(ctx context.Context) {
		switch {
		case errors.Is(ctx.Err(), context.Canceled):
			l.Error(ctx, fmt.Sprintf("Build failed: %s", buildcache.CanceledBuildReason))
		case e != nil:
			l.Error(ctx, fmt.Sprintf("Build failed: %v", e))
		default:
			l.Info(ctx, fmt.Sprintf("Build finished, took %s",
				time.Since(startTime).Truncate(time.Second).String()))
		}
	}(ctx)

	defer func() {
		if r := recover(); r != nil {
			telemetry.ReportCriticalError(ctx, "recovered from panic in template build", nil, attribute.String("panic", fmt.Sprintf("%v", r)), telemetry.WithTemplateID(cfg.TemplateID), telemetry.WithBuildID(template.BuildID))
			e = errors.New("fatal error occurred during template build, please contact us")
		}
	}()

	if isV1Build {
		hookedCore, done := writer.NewPostProcessor(ctx, progressDelay, logsCore)
		defer done()
		l = logger.NewTracedLoggerFromCore(hookedCore)
	}

	l.Info(ctx, fmt.Sprintf("Building template %s/%s", cfg.TemplateID, template.BuildID))

	defer func(ctx context.Context) {
		if e == nil {
			return
		}

		// Remove build files if build fails
		removeErr := b.templateStorage.DeleteObjectsWithPrefix(ctx, template.BuildID)
		if removeErr != nil {
			e = errors.Join(e, fmt.Errorf("error removing build files: %w", removeErr))
		}
	}(context.WithoutCancel(ctx))

	envdVersion, err := envd.GetEnvdVersion(ctx, b.config.HostEnvdPath)
	if err != nil {
		return nil, fmt.Errorf("error getting envd version: %w", err)
	}

	uploadErrGroup := &errgroup.Group{}
	defer func() {
		// Wait for all template layers to be uploaded even if the build fails
		err := uploadErrGroup.Wait()
		if err != nil {
			e = errors.Join(e, fmt.Errorf("error uploading template layers: %w", err))
		}
	}()

	buildContext := buildcontext.BuildContext{
		BuilderConfig:  b.config,
		Config:         cfg,
		Template:       template,
		UploadErrGroup: uploadErrGroup,
		EnvdVersion:    envdVersion,
		CacheScope:     cacheScope,
		IsV1Build:      isV1Build,
		Version:        cfg.Version,
	}

	return runBuild(ctx, l, buildContext, b)
}

func runBuild(
	ctx context.Context,
	userLogger logger.Logger,
	bc buildcontext.BuildContext,
	builder *Builder,
) (*Result, error) {
	index := cache.NewHashIndex(bc.CacheScope, builder.buildStorage, builder.templateStorage)

	layerExecutor := layer.NewLayerExecutor(
		bc,
		builder.logger,
		builder.templateCache,
		builder.proxy,
		builder.sandboxes,
		builder.templateStorage,
		builder.buildStorage,
		index,
	)

	baseBuilder := base.New(
		bc,
		builder.featureFlags,
		builder.logger,
		builder.proxy,
		builder.templateStorage,
		builder.artifactRegistry,
		builder.dockerhubRepository,
		layerExecutor,
		index,
		builder.metrics,
		builder.sandboxFactory,
	)

	commandExecutor := commands.NewCommandExecutor(
		bc,
		builder.buildStorage,
		builder.proxy,
	)

	userBuilder := user.New(
		bc,
		builder.sandboxFactory,
		builder.logger,
		builder.proxy,
		layerExecutor,
		commandExecutor,
		index,
		builder.metrics,
		config.TemplateDefaultUser,
		bc.Config.Force,
	)

	stepBuilders := steps.CreateStepPhases(
		bc,
		builder.sandboxFactory,
		builder.logger,
		builder.proxy,
		layerExecutor,
		commandExecutor,
		index,
		builder.metrics,
	)

	postProcessingBuilder := finalize.New(
		bc,
		builder.sandboxFactory,
		builder.templateStorage,
		builder.proxy,
		layerExecutor,
		builder.featureFlags,
		builder.logger,
	)

	// Construct the phases/steps to run
	builders := []phases.BuilderPhase{
		baseBuilder,
	}
	// Default user is only set for version TemplateDefaultUserVersion
	ok, err := utils.IsGTEVersion(bc.Version, templates.TemplateV2ReleaseVersion)
	if err != nil {
		return nil, fmt.Errorf("error checking build version: %w", err)
	}
	if ok {
		builders = append(builders, userBuilder)
	}
	builders = append(builders, stepBuilders...)
	builders = append(builders, postProcessingBuilder)

	lastLayerResult, err := phases.Run(ctx, userLogger, bc, builder.metrics, builders)
	if err != nil {
		return nil, err
	}

	// Ensure the base layer is uploaded before getting the rootfs size
	err = bc.UploadErrGroup.Wait()
	if err != nil {
		return nil, fmt.Errorf("error waiting for layers upload: %w", err)
	}

	// Get the base rootfs size from the template files
	// This is the size of the rootfs after provisioning and before building the layers
	// (as they don't change the rootfs size)
	rootfsSize, err := getRootfsSize(ctx, builder.templateStorage, lastLayerResult.Metadata.Template)
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs size: %w", err)
	}
	logger.L().Info(ctx, "rootfs size", zap.Uint64("size", rootfsSize))

	return &Result{
		EnvdVersion:  bc.EnvdVersion,
		RootfsSizeMB: int64(rootfsSize >> constants.ToMBShift),
	}, nil
}

// forceSteps sets force for all steps after the first encounter.
func forceSteps(template config.TemplateConfig) config.TemplateConfig {
	shouldRebuild := template.Force != nil && *template.Force
	for _, step := range template.Steps {
		// Force rebuild if the step has a Force flag set to true
		if step.Force != nil && step.GetForce() {
			shouldRebuild = true
		}

		if !shouldRebuild {
			continue
		}

		force := true
		step.Force = &force
	}

	return template
}

func getRootfsSize(
	ctx context.Context,
	s storage.StorageProvider,
	metadata storage.TemplateFiles,
) (uint64, error) {
	obj, err := s.OpenObject(ctx, metadata.StorageRootfsHeaderPath(), storage.RootFSHeaderObjectType)
	if err != nil {
		return 0, fmt.Errorf("error opening rootfs header object: %w", err)
	}

	h, err := header.Deserialize(ctx, obj)
	if err != nil {
		return 0, fmt.Errorf("error deserializing rootfs header: %w", err)
	}

	return h.Metadata.Size, nil
}
