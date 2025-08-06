package build

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type BuilderPhase interface {
	Build(ctx context.Context, lastStepResult LayerResult) (LayerResult, error)
}

type LayerResult struct {
	Metadata LayerMetadata
	Cached   bool
	Hash     string

	StartMetadata *StartMetadata
}

type BuildContext struct {
	Config         config.TemplateConfig
	Template       storage.TemplateFiles
	UserLogger     *writer.PostProcessor
	UploadErrGroup *errgroup.Group
	EnvdVersion    string
	CacheScope     string
	IsV1Build      bool
}

type Builder struct {
	logger *zap.Logger
	tracer trace.Tracer

	templateStorage  storage.StorageProvider
	buildStorage     storage.StorageProvider
	devicePool       *nbd.DevicePool
	networkPool      *network.Pool
	artifactRegistry artifactsregistry.ArtifactsRegistry
	proxy            *proxy.SandboxProxy
	sandboxes        *smap.Map[*sandbox.Sandbox]
	templateCache    *sbxtemplate.Cache
}

const (
	templatesDirectory = "/orchestrator/build-templates"

	rootfsBuildFileName = "rootfs.ext4.build"
	rootfsProvisionLink = "rootfs.ext4.build.provision"

	systemdInitPath = "/sbin/init"

	provisionTimeout = 5 * time.Minute
	waitEnvdTimeout  = 60 * time.Second

	baseLayerTimeout = 10 * time.Minute
)

var defaultUser = "root"

func NewBuilder(
	logger *zap.Logger,
	tracer trace.Tracer,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	devicePool *nbd.DevicePool,
	networkPool *network.Pool,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	templateCache *sbxtemplate.Cache,
) *Builder {
	return &Builder{
		logger:           logger,
		tracer:           tracer,
		templateStorage:  templateStorage,
		buildStorage:     buildStorage,
		artifactRegistry: artifactRegistry,
		devicePool:       devicePool,
		networkPool:      networkPool,
		proxy:            proxy,
		sandboxes:        sandboxes,
		templateCache:    templateCache,
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
func (b *Builder) Build(ctx context.Context, finalMetadata storage.TemplateFiles, template config.TemplateConfig, logsWriter *zap.Logger) (r *Result, e error) {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	cacheScope := template.CacheScope

	// Validate template, update force layers if needed
	template = forceSteps(template)

	isV1Build := template.FromImage == "" && template.FromTemplate == nil

	postProcessor := writer.NewPostProcessor(ctx, logsWriter, isV1Build)
	go postProcessor.Start()
	defer func() {
		postProcessor.Stop(ctx, e)
	}()

	postProcessor.Info(fmt.Sprintf("Building template %s/%s", finalMetadata.TemplateID, finalMetadata.BuildID))

	defer func(ctx context.Context) {
		if e == nil {
			return
		}
		// Remove build files if build fails
		removeErr := b.templateStorage.DeleteObjectsWithPrefix(ctx, finalMetadata.BuildID)
		if removeErr != nil {
			e = errors.Join(e, fmt.Errorf("error removing build files: %w", removeErr))
		}
	}(context.WithoutCancel(ctx))

	envdVersion, err := envd.GetEnvdVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting envd version: %w", err)
	}

	uploadErrGroup, _ := errgroup.WithContext(ctx)
	defer func() {
		// Wait for all template layers to be uploaded even if the build fails
		err := uploadErrGroup.Wait()
		if err != nil {
			e = errors.Join(e, fmt.Errorf("error uploading template layers: %w", err))
		}
	}()

	buildContext := BuildContext{
		Config:         template,
		Template:       finalMetadata,
		UserLogger:     postProcessor,
		UploadErrGroup: uploadErrGroup,
		EnvdVersion:    envdVersion,
		CacheScope:     cacheScope,
		IsV1Build:      isV1Build,
	}

	res, err := buildContext.runBuild(ctx, b)
	if err != nil {
		return nil, fmt.Errorf("error running build: %w", err)
	}

	return res, nil
}

func (bc BuildContext) runBuild(
	ctx context.Context,
	builder *Builder,
) (*Result, error) {
	layerExecutor := NewLayerExecutor(bc,
		builder.tracer,
		builder.networkPool,
		builder.devicePool,
		builder.templateCache,
		builder.proxy,
		builder.sandboxes,
		builder.templateStorage,
		builder.buildStorage,
	)

	baseBuilder := NewBaseBuilder(bc,
		builder.logger,
		builder.tracer,
		builder.templateStorage,
		builder.buildStorage,
		builder.devicePool,
		builder.networkPool,
		builder.artifactRegistry,
		layerExecutor,
	)

	stepsBuilder := NewStepsBuilder(
		bc,
		builder.logger,
		builder.tracer,
		builder.templateStorage,
		builder.buildStorage,
		builder.proxy,
		layerExecutor,
	)

	postProcessingBuilder := NewPostProcessingBuilder(
		bc,
		builder.logger,
		builder.tracer,
		builder.templateStorage,
		builder.proxy,
		layerExecutor,
	)

	builders := []BuilderPhase{
		baseBuilder,
		stepsBuilder,
		postProcessingBuilder,
	}

	lastLayerResult := LayerResult{}
	for _, b := range builders {
		res, err := b.Build(ctx, lastLayerResult)
		if err != nil {
			return nil, fmt.Errorf("error building layer: %w", err)
		}

		lastLayerResult = res
	}

	// Ensure the base layer is uploaded before getting the rootfs size
	err := bc.UploadErrGroup.Wait()
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

	var fromTemplateMetadata *FromTemplateMetadata
	if bc.Config.FromTemplate != nil {
		fromTemplateMetadata = &FromTemplateMetadata{
			Alias:   bc.Config.FromTemplate.GetAlias(),
			BuildID: bc.Config.FromTemplate.BuildID,
		}
	}
	err = saveTemplateMetadata(ctx, builder.templateStorage, bc.Template.BuildID, TemplateMetadata{
		Template:     lastLayerResult.Metadata.Template,
		Metadata:     lastLayerResult.Metadata.CmdMeta,
		FromImage:    &bc.Config.FromImage,
		FromTemplate: fromTemplateMetadata,
		Start:        lastLayerResult.StartMetadata,
	})
	if err != nil {
		return nil, fmt.Errorf("error saving template metadata: %w", err)
	}

	return &Result{
		EnvdVersion:  bc.EnvdVersion,
		RootfsSizeMB: int64(rootfsSize >> constants.ToMBShift),
	}, nil
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

	h, err := header.Deserialize(obj)
	if err != nil {
		return 0, fmt.Errorf("error deserializing rootfs header: %w", err)
	}

	return h.Metadata.Size, nil
}

func isCached(
	ctx context.Context,
	s storage.StorageProvider,
	metadata LayerMetadata,
) (bool, error) {
	_, err := getRootfsSize(ctx, s, metadata.Template)
	if err != nil {
		// If the rootfs header does not exist, the layer is not cached
		return false, nil
	} else {
		// If the rootfs header exists, the layer is cached
		return true, nil
	}
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

func layerInfo(
	cached bool,
	prefix string,
	text string,
	hash string,
) string {
	cachedPrefix := ""
	if cached {
		cachedPrefix = "CACHED "
	}
	return fmt.Sprintf("%s[%s] %s [%s]", cachedPrefix, prefix, text, hash)
}

// syncChangesToDisk synchronizes filesystem changes to the filesystem
// This is useful to ensure that all changes made in the sandbox are written to disk
// to be able to re-create the sandbox without resume.
func syncChangesToDisk(
	ctx context.Context,
	tracer trace.Tracer,
	proxy *proxy.SandboxProxy,
	sandboxID string,
) error {
	return sandboxtools.RunCommand(
		ctx,
		tracer,
		proxy,
		sandboxID,
		"sync",
		sandboxtools.CommandMetadata{
			User: "root",
		},
	)
}
