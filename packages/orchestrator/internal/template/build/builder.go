package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

const progressDelay = 5 * time.Second

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build")

type Builder struct {
	logger *zap.Logger

	templateStorage  storage.StorageProvider
	buildStorage     storage.StorageProvider
	devicePool       *nbd.DevicePool
	networkPool      *network.Pool
	artifactRegistry artifactsregistry.ArtifactsRegistry
	proxy            *proxy.SandboxProxy
	sandboxes        *smap.Map[*sandbox.Sandbox]
	templateCache    *sbxtemplate.Cache
	metrics          *metrics.BuildMetrics
}

func NewBuilder(
	logger *zap.Logger,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	devicePool *nbd.DevicePool,
	networkPool *network.Pool,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	templateCache *sbxtemplate.Cache,
	buildMetrics *metrics.BuildMetrics,
) *Builder {
	return &Builder{
		logger:           logger,
		templateStorage:  templateStorage,
		buildStorage:     buildStorage,
		artifactRegistry: artifactRegistry,
		devicePool:       devicePool,
		networkPool:      networkPool,
		proxy:            proxy,
		sandboxes:        sandboxes,
		templateCache:    templateCache,
		metrics:          buildMetrics,
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
func (b *Builder) Build(ctx context.Context, template storage.TemplateFiles, config config.TemplateConfig, logsCore zapcore.Core) (r *Result, e error) {
	ctx, childSpan := tracer.Start(ctx, "build")
	defer childSpan.End()

	// Record build duration and result at the end
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		success := e == nil && r != nil
		b.metrics.RecordBuildDuration(ctx, duration, success)

		if success {
			b.metrics.RecordBuildResult(ctx, true)
			b.metrics.RecordRootfsSize(ctx, r.RootfsSizeMB<<constants.ToMBShift)
		} else if !errors.Is(e, context.Canceled) {
			// Skip reporting failure metrics only on explicit cancellation
			b.metrics.RecordBuildResult(ctx, false)
		}
	}()

	cacheScope := config.CacheScope

	// Validate template, update force layers if needed
	config = forceSteps(config)

	isV1Build := config.FromImage == "" && config.FromTemplate == nil

	logger := zap.New(logsCore)
	defer func() {
		if e != nil {
			logger.Error(fmt.Sprintf("Build failed: %v", e))
		} else {
			logger.Info(fmt.Sprintf("Build finished, took %s",
				time.Since(startTime).Truncate(time.Second).String()))
		}
	}()

	if isV1Build {
		hookedCore, done := writer.NewPostProcessor(progressDelay, logsCore)
		defer done()
		logger = zap.New(hookedCore)
	}

	logger.Info(fmt.Sprintf("Building template %s/%s", config.TemplateID, template.BuildID))

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

	envdVersion, err := envd.GetEnvdVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting envd version: %w", err)
	}

	var uploadErrGroup errgroup.Group
	defer func() {
		// Wait for all template layers to be uploaded even if the build fails
		err := uploadErrGroup.Wait()
		if err != nil {
			e = errors.Join(e, fmt.Errorf("error uploading template layers: %w", err))
		}
	}()

	buildContext := buildcontext.BuildContext{
		Config:         config,
		Template:       template,
		UploadErrGroup: &uploadErrGroup,
		EnvdVersion:    envdVersion,
		CacheScope:     cacheScope,
		IsV1Build:      isV1Build,
	}

	return runBuild(ctx, logger, buildContext, b)
}

func runBuild(
	ctx context.Context,
	userLogger *zap.Logger,
	bc buildcontext.BuildContext,
	builder *Builder,
) (*Result, error) {
	index := cache.NewHashIndex(bc.CacheScope, builder.buildStorage, builder.templateStorage)

	layerExecutor := layer.NewLayerExecutor(bc,
		builder.logger,
		builder.networkPool,
		builder.devicePool,
		builder.templateCache,
		builder.proxy,
		builder.sandboxes,
		builder.templateStorage,
		builder.buildStorage,
		index,
	)

	baseBuilder := base.New(
		bc,
		builder.logger,
		builder.proxy,
		builder.templateStorage,
		builder.devicePool,
		builder.networkPool,
		builder.artifactRegistry,
		layerExecutor,
		index,
		builder.metrics,
	)

	commandExecutor := commands.NewCommandExecutor(
		bc,
		builder.buildStorage,
		builder.proxy,
	)

	stepBuilders := steps.CreateStepPhases(
		bc,
		builder.logger,
		builder.proxy,
		layerExecutor,
		commandExecutor,
		index,
		builder.metrics,
	)

	postProcessingBuilder := finalize.New(
		bc,
		builder.templateStorage,
		builder.proxy,
		layerExecutor,
	)

	// Construct the phases/steps to run
	builders := []phases.BuilderPhase{
		baseBuilder,
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
	zap.L().Info("rootfs size", zap.Uint64("size", rootfsSize))

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
		if step.Force != nil && *step.Force {
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
	obj, err := s.OpenObject(ctx, metadata.StorageRootfsHeaderPath())
	if err != nil {
		return 0, fmt.Errorf("error opening rootfs header object: %w", err)
	}

	h, err := header.Deserialize(ctx, obj)
	if err != nil {
		return 0, fmt.Errorf("error deserializing rootfs header: %w", err)
	}

	return h.Metadata.Size, nil
}
