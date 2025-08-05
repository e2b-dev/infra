package build

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/envd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type LayerResult struct {
	Metadata LayerMetadata
	Cached   bool
	Hash     string
}

type BuildContext struct {
	Config         config.TemplateConfig
	Template       storage.TemplateFiles
	Logger         *writer.PostProcessor
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
		Logger:         postProcessor,
		UploadErrGroup: uploadErrGroup,
		EnvdVersion:    envdVersion,
		CacheScope:     cacheScope,
		IsV1Build:      isV1Build,
	}

	res, err := runBuild(
		ctx,
		b,
		buildContext,
	)
	if err != nil {
		return nil, fmt.Errorf("error running build: %w", err)
	}

	return res, nil
}

func runStepsBuild(
	ctx context.Context,
	b *Builder,
	bc BuildContext,
	lastStepResult LayerResult,
) (LayerResult, error) {
	sourceLayer := lastStepResult

	baseTemplateID := lastStepResult.Metadata.Template.TemplateID

	for i, step := range bc.Config.Steps {
		currentLayer, err := b.shouldBuildStep(
			ctx,
			bc,
			sourceLayer,
			step,
		)
		if err != nil {
			return LayerResult{}, fmt.Errorf("error checking if step %d should be built: %w", i+1, err)
		}

		// If the last layer is cached, update the base metadata to the step metadata
		// This is needed to properly run the sandbox for the next step
		if sourceLayer.Cached {
			baseTemplateID = currentLayer.Metadata.Template.TemplateID
		}

		prefix := fmt.Sprintf("builder %d/%d", i+1, len(bc.Config.Steps))
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))
		bc.Logger.Info(layerInfo(currentLayer.Cached, prefix, cmd, currentLayer.Hash))

		if currentLayer.Cached {
			sourceLayer = currentLayer
		}

		res, err := b.buildStep(
			ctx,
			bc,
			step,
			prefix,
			baseTemplateID,
			sourceLayer,
			currentLayer,
		)
		if err != nil {
			return LayerResult{}, fmt.Errorf("error building step %d: %w", i+1, err)
		}

		sourceLayer = res
	}

	return sourceLayer, nil
}

// runPostProcessing runs post-processing actions in the sandbox
func runPostProcessing(
	ctx context.Context,
	b *Builder,
	bc BuildContext,
	lastStepResult LayerResult,
) (LayerResult, *StartMetadata, error) {
	var startMetadata *StartMetadata
	if bc.Config.StartCmd != "" || bc.Config.ReadyCmd != "" {
		startMetadata = &StartMetadata{
			StartCmd: bc.Config.StartCmd,
			ReadyCmd: bc.Config.ReadyCmd,
			Metadata: lastStepResult.Metadata.CmdMeta,
		}
	}

	// If the template is built from another template, and the start metadata are not set,
	// use the start metadata from the template it is built from.
	if startMetadata == nil && bc.Config.FromTemplate != nil {
		tm, err := ReadTemplateMetadata(ctx, b.templateStorage, bc.Config.FromTemplate.BuildID)
		if err != nil {
			return LayerResult{}, nil, fmt.Errorf("error reading from template metadata: %w", err)
		}
		startMetadata = tm.Start
	}

	hash := hashKeys(lastStepResult.Hash, "config-run-cmd")
	finalLayer, err := b.buildLayer(
		ctx,
		bc,
		hash,
		lastStepResult.Metadata.Template,
		bc.Template,
		lastStepResult.Cached,
		func(
			context context.Context,
			b *Builder,
			t sbxtemplate.Template,
			exportTemplate storage.TemplateFiles,
		) (*sandbox.Sandbox, error) {
			// Always restart the sandbox for the final layer to properly wire the rootfs path for the final template.
			return createSandboxFromTemplate(ctx, b, t, sandbox.Config{
				Vcpu:      bc.Config.VCpuCount,
				RamMB:     bc.Config.MemoryMB,
				HugePages: bc.Config.HugePages,

				AllowInternetAccess: &globalconfig.AllowSandboxInternet,

				Envd: sandbox.EnvdMetadata{
					Version: bc.EnvdVersion,
				},
			}, exportTemplate)
		},
		b.postProcessingFn(bc, lastStepResult.Metadata.CmdMeta, startMetadata),
	)
	if err != nil {
		return LayerResult{}, nil, fmt.Errorf("error running start and ready commands in sandbox: %w", err)
	}

	return LayerResult{
		Metadata: finalLayer,
		Cached:   false,
		Hash:     hash,
	}, startMetadata, nil
}

func runBaseLayerBuild(
	ctx context.Context,
	b *Builder,
	bc BuildContext,
) (LayerResult, error) {
	hash, err := hashBase(bc.Config)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error getting base hash: %w", err)
	}

	cached, baseMetadata, err := b.setupBase(ctx, bc, hash)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error setting up build: %w", err)
	}

	// Print the base layer information
	var baseSource string
	if bc.Config.FromTemplate != nil {
		baseSource = "FROM TEMPLATE " + bc.Config.FromTemplate.GetAlias()
	} else {
		fromImage := bc.Config.FromImage
		if fromImage == "" {
			tag, err := b.artifactRegistry.GetTag(ctx, bc.Template.TemplateID, bc.Template.BuildID)
			if err != nil {
				return LayerResult{}, fmt.Errorf("error getting tag for template: %w", err)
			}
			fromImage = tag
		}
		baseSource = "FROM " + fromImage
	}
	bc.Logger.Info(layerInfo(cached, "base", baseSource, hash))

	if cached {
		return LayerResult{
			Metadata: baseMetadata,
			Cached:   true,
			Hash:     hash,
		}, nil
	}

	baseMetadata, err = b.buildBaseLayer(
		ctx,
		bc,
		baseMetadata,
		hash,
	)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error building base layer: %w", err)
	}

	return LayerResult{
		Metadata: baseMetadata,
		Cached:   false,
		Hash:     hash,
	}, nil
}

func runBuild(
	ctx context.Context,
	b *Builder,
	bc BuildContext,
) (*Result, error) {
	baseStepResult, err := runBaseLayerBuild(ctx, b, bc)
	if err != nil {
		return nil, fmt.Errorf("error building base layer: %w", err)
	}

	stepsResult, err := runStepsBuild(ctx, b, bc, baseStepResult)
	if err != nil {
		return nil, fmt.Errorf("error running layers build: %w", err)
	}

	postProcessingResult, startCmdMeta, err := runPostProcessing(ctx, b, bc, stepsResult)
	if err != nil {
		return nil, fmt.Errorf("error running post-processing phase: %w", err)
	}

	// Ensure the base layer is uploaded before getting the rootfs size
	err = bc.UploadErrGroup.Wait()
	if err != nil {
		return nil, fmt.Errorf("error waiting for layers upload: %w", err)
	}

	// Get the base rootfs size from the template files
	// This is the size of the rootfs after provisioning and before building the layers
	// (as they don't change the rootfs size)
	rootfsSize, err := getRootfsSize(ctx, b.templateStorage, baseStepResult.Metadata.Template)
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs size: %w", err)
	}

	var fromTemplateMetadata *FromTemplateMetadata
	if bc.Config.FromTemplate != nil {
		fromTemplateMetadata = &FromTemplateMetadata{
			Alias:   bc.Config.FromTemplate.GetAlias(),
			BuildID: bc.Config.FromTemplate.BuildID,
		}
	}
	err = saveTemplateMetadata(ctx, b.templateStorage, bc.Template.BuildID, TemplateMetadata{
		Template:     postProcessingResult.Metadata.Template,
		Metadata:     postProcessingResult.Metadata.CmdMeta,
		FromImage:    &bc.Config.FromImage,
		FromTemplate: fromTemplateMetadata,
		Start:        startCmdMeta,
	})
	if err != nil {
		return nil, fmt.Errorf("error saving template metadata: %w", err)
	}

	return &Result{
		EnvdVersion:  bc.EnvdVersion,
		RootfsSizeMB: int64(rootfsSize >> constants.ToMBShift),
	}, nil
}

func (b *Builder) shouldBuildStep(
	ctx context.Context,
	bc BuildContext,
	sourceLayer LayerResult,
	step *templatemanager.TemplateStep,
) (LayerResult, error) {
	hash := hashStep(sourceLayer.Hash, step)

	force := step.Force != nil && *step.Force
	if !force {
		m, err := layerMetaFromHash(ctx, b.buildStorage, bc.CacheScope, hash)
		if err != nil {
			b.logger.Info("layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash), zap.String("step", step.Type))
		} else {
			// Check if the layer is cached
			found, err := isCached(ctx, b.templateStorage, m)
			if err != nil {
				return LayerResult{}, fmt.Errorf("error checking if layer is cached: %w", err)
			}

			if found {
				return LayerResult{
					Metadata: m,
					Cached:   true,
					Hash:     hash,
				}, nil
			}
		}
	}

	meta := LayerMetadata{
		Template: storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      sourceLayer.Metadata.Template.KernelVersion,
			FirecrackerVersion: sourceLayer.Metadata.Template.FirecrackerVersion,
		},
		CmdMeta: sourceLayer.Metadata.CmdMeta,
	}

	if sourceLayer.Cached {
		meta.Template = storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      bc.Template.KernelVersion,
			FirecrackerVersion: bc.Template.FirecrackerVersion,
		}
	}

	return LayerResult{
		Metadata: meta,
		Cached:   false,
		Hash:     hash,
	}, nil
}

func (b *Builder) buildStep(
	ctx context.Context,
	bc BuildContext,
	step *templatemanager.TemplateStep,
	prefix string,
	baseTemplateID string,
	sourceLayer LayerResult,
	currentLayer LayerResult,
) (LayerResult, error) {
	meta, err := b.buildLayer(
		ctx,
		bc,
		currentLayer.Hash,
		sourceLayer.Metadata.Template,
		currentLayer.Metadata.Template,
		sourceLayer.Cached,
		func(
			context context.Context,
			b *Builder,
			t sbxtemplate.Template,
			exportTemplate storage.TemplateFiles,
		) (*sandbox.Sandbox, error) {
			sbxConfig := sandbox.Config{
				BaseTemplateID: baseTemplateID,

				Vcpu:      bc.Config.VCpuCount,
				RamMB:     bc.Config.MemoryMB,
				HugePages: bc.Config.HugePages,

				AllowInternetAccess: &globalconfig.AllowSandboxInternet,

				Envd: sandbox.EnvdMetadata{
					Version: bc.EnvdVersion,
				},
			}

			// First not cached layer is create (to change CPU, etc), subsequent are resumes.
			if sourceLayer.Cached {
				return createSandboxFromTemplate(ctx, b, t, sbxConfig, exportTemplate)
			} else {
				return resumeSandbox(ctx, b, t, sbxConfig)
			}
		},
		func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error) {
			bc.Logger.Debug(fmt.Sprintf("Running action in: %s/%s", sourceLayer.Metadata.Template.TemplateID, sourceLayer.Metadata.Template.BuildID))

			meta, err := b.applyCommand(ctx, bc.Logger, bc.CacheScope, sbx, prefix, step, sourceLayer.Metadata.CmdMeta)
			if err != nil {
				return sandboxtools.CommandMetadata{}, fmt.Errorf("error processing layer: %w", err)
			}

			err = syncChangesToDisk(
				ctx,
				b.tracer,
				b.proxy,
				sbx.Runtime.SandboxID,
			)
			if err != nil {
				return sandboxtools.CommandMetadata{}, fmt.Errorf("error running sync command: %w", err)
			}

			return meta, nil
		},
	)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error running build layer: %w", err)
	}

	return LayerResult{
		Metadata: meta,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (b *Builder) buildBaseLayer(
	ctx context.Context,
	bc BuildContext,
	baseMetadata LayerMetadata,
	hash string,
) (LayerMetadata, error) {
	templateBuildDir := filepath.Join(templatesDirectory, bc.Template.BuildID)
	err := os.MkdirAll(templateBuildDir, 0o777)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating template build directory: %w", err)
	}
	defer func() {
		err := os.RemoveAll(templateBuildDir)
		if err != nil {
			b.logger.Error("Error while removing template build directory", zap.Error(err))
		}
	}()

	// Created here to be able to pass it to CreateSandbox for populating COW cache
	rootfsPath := filepath.Join(templateBuildDir, rootfsBuildFileName)

	rootfs, memfile, envsImg, err := constructBaseLayerFiles(
		ctx,
		b.tracer,
		bc,
		baseMetadata.Template.BuildID,
		b.artifactRegistry,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error building environment: %w", err)
	}

	// Env variables from the Docker image
	baseMetadata.CmdMeta.EnvVars = oci.ParseEnvs(envsImg.Env)

	cacheFiles, err := baseMetadata.Template.CacheFiles()
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating template files: %w", err)
	}
	localTemplate := sbxtemplate.NewLocalTemplate(cacheFiles, rootfs, memfile)
	defer localTemplate.Close()

	// Provision sandbox with systemd and other vital parts
	bc.Logger.Info("Provisioning sandbox template")
	// Just a symlink to the rootfs build file, so when the COW cache deletes the underlying file (here symlink),
	// it will not delete the rootfs file. We use the rootfs again later on to start the sandbox template.
	rootfsProvisionPath := filepath.Join(templateBuildDir, rootfsProvisionLink)
	err = os.Symlink(rootfsPath, rootfsProvisionPath)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating provision rootfs: %w", err)
	}

	// Allow sandbox internet access during provisioning
	allowInternetAccess := true

	baseSbxConfig := sandbox.Config{
		BaseTemplateID: baseMetadata.Template.TemplateID,

		Vcpu:      bc.Config.VCpuCount,
		RamMB:     bc.Config.MemoryMB,
		HugePages: bc.Config.HugePages,

		AllowInternetAccess: &allowInternetAccess,

		Envd: sandbox.EnvdMetadata{
			Version: bc.EnvdVersion,
		},
	}
	fcVersions := fc.FirecrackerVersions{
		KernelVersion:      bc.Template.KernelVersion,
		FirecrackerVersion: bc.Template.FirecrackerVersion,
	}
	err = b.provisionSandbox(
		ctx,
		bc.Logger,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		fcVersions,
		localTemplate,
		rootfsProvisionPath,
		provisionScriptResultPath,
		provisionLogPrefix,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error provisioning sandbox: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
	if err != nil {
		zap.L().Error("provisioned filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		return LayerMetadata{}, fmt.Errorf("error checking provisioned filesystem integrity: %w", err)
	}
	zap.L().Debug("provisioned filesystem ext4 integrity",
		zap.String("result", ext4Check),
	)

	err = b.enlargeDiskAfterProvisioning(ctx, bc.Config, rootfs)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error enlarging disk after provisioning: %w", err)
	}

	// Create sandbox for building template
	bc.Logger.Debug("Creating base sandbox template layer")

	// TODO: Temporarily set this based on global config, should be removed later (it should be passed as a parameter in build)
	baseSbxConfig.AllowInternetAccess = &globalconfig.AllowSandboxInternet
	sourceSbx, err := sandbox.CreateSandbox(
		ctx,
		b.tracer,
		b.networkPool,
		b.devicePool,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		fcVersions,
		localTemplate,
		baseLayerTimeout,
		rootfsPath,
		fc.ProcessOptions{
			InitScriptPath:      systemdInitPath,
			KernelLogs:          env.IsDevelopment(),
			SystemdToKernelLogs: false,
		},
		nil,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating sandbox: %w", err)
	}
	defer sourceSbx.Stop(ctx)

	err = sourceSbx.WaitForEnvd(
		ctx,
		b.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	err = pauseAndUpload(
		ctx,
		b,
		bc,
		sourceSbx,
		hash,
		baseMetadata,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error pausing and uploading template: %w", err)
	}

	return baseMetadata, nil
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

func (b *Builder) setupBase(
	ctx context.Context,
	buildContext BuildContext,
	hash string,
) (bool, LayerMetadata, error) {
	switch {
	case buildContext.Config.FromTemplate != nil:
		// If the template is built from another template, use its metadata
		tm, err := ReadTemplateMetadata(ctx, b.templateStorage, buildContext.Config.FromTemplate.BuildID)
		if err != nil {
			return false, LayerMetadata{}, fmt.Errorf("error getting base layer from cache, you may need to rebuild the base template: %w", err)
		}

		return true, LayerMetadata{
			Template: tm.Template,
			CmdMeta:  tm.Metadata,
		}, nil
	default:
		cmdMeta := sandboxtools.CommandMetadata{
			User:    defaultUser,
			WorkDir: nil,
			EnvVars: make(map[string]string),
		}

		// This is a compatibility for v1 template builds
		if buildContext.IsV1Build {
			cwd := "/home/user"
			cmdMeta.WorkDir = &cwd
		}

		var baseMetadata LayerMetadata
		bm, err := layerMetaFromHash(ctx, b.buildStorage, buildContext.CacheScope, hash)
		if err != nil {
			b.logger.Info("base layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))

			baseMetadata = LayerMetadata{
				Template: storage.TemplateFiles{
					TemplateID:         id.Generate(),
					BuildID:            uuid.New().String(),
					KernelVersion:      buildContext.Template.KernelVersion,
					FirecrackerVersion: buildContext.Template.FirecrackerVersion,
				},
				CmdMeta: cmdMeta,
			}
		} else {
			baseMetadata = bm
		}

		// Invalidate base cache
		if buildContext.Config.Force != nil && *buildContext.Config.Force {
			baseMetadata = LayerMetadata{
				Template: storage.TemplateFiles{
					TemplateID:         id.Generate(),
					BuildID:            uuid.New().String(),
					KernelVersion:      buildContext.Template.KernelVersion,
					FirecrackerVersion: buildContext.Template.FirecrackerVersion,
				},
				CmdMeta: cmdMeta,
			}
		}

		baseCached, err := isCached(ctx, b.templateStorage, baseMetadata)
		if err != nil {
			return false, LayerMetadata{}, fmt.Errorf("error checking if base layer is cached: %w", err)
		}

		return baseCached, baseMetadata, nil
	}
}

func (b *Builder) postProcessingFn(
	bc BuildContext,
	cmdMeta sandboxtools.CommandMetadata,
	start *StartMetadata,
) func(ctx context.Context, sbx *sandbox.Sandbox) (sandboxtools.CommandMetadata, error) {
	return func(ctx context.Context, sbx *sandbox.Sandbox) (cm sandboxtools.CommandMetadata, e error) {
		defer func() {
			if e != nil {
				return
			}

			// Ensure all changes are synchronized to disk so the sandbox can be restarted
			err := syncChangesToDisk(
				ctx,
				b.tracer,
				b.proxy,
				sbx.Runtime.SandboxID,
			)
			if err != nil {
				e = fmt.Errorf("error running sync command: %w", err)
				return
			}
		}()

		// Run configuration script
		err := runConfiguration(
			ctx,
			b.tracer,
			b.proxy,
			bc.Logger,
			bc.Template,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running configuration script: %w", err)
		}

		if start == nil {
			return cmdMeta, nil
		}

		// Start command
		commandsCtx, commandsCancel := context.WithCancel(ctx)
		defer commandsCancel()

		var startCmdRun errgroup.Group
		startCmdConfirm := make(chan struct{})
		if start.StartCmd != "" {
			bc.Logger.Info("Running start command")
			startCmdRun.Go(func() error {
				err := sandboxtools.RunCommandWithConfirmation(
					commandsCtx,
					b.tracer,
					b.proxy,
					bc.Logger,
					zapcore.InfoLevel,
					"start",
					sbx.Runtime.SandboxID,
					start.StartCmd,
					start.Metadata,
					startCmdConfirm,
				)
				// If the ctx is canceled, the ready command succeeded and no start command await is necessary.
				if err != nil && !errors.Is(err, context.Canceled) {
					// Cancel the ready command context, so the ready command does not wait anymore if an error occurs.
					commandsCancel()
					return fmt.Errorf("error running start command: %w", err)
				}

				return nil
			})
		} else {
			// If no start command is defined, we still need to confirm that the start command has started.
			close(startCmdConfirm)
		}

		// Ready command
		readyCmd := start.ReadyCmd
		if readyCmd == "" {
			if start.StartCmd == "" {
				readyCmd = "sleep 0"
			} else {
				readyCmd = GetDefaultReadyCommand(bc.Template)
			}
		}
		err = b.runReadyCommand(
			commandsCtx,
			bc.Logger,
			sbx.Runtime.SandboxID,
			readyCmd,
			start.Metadata,
		)
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running ready command: %w", err)
		}

		// Wait for the start command to start executing.
		select {
		case <-ctx.Done():
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error waiting for start command: %w", commandsCtx.Err())
		case <-startCmdConfirm:
		}
		// Cancel the start command context (it's running in the background anyway).
		// If it has already finished, check the error.
		commandsCancel()
		err = startCmdRun.Wait()
		if err != nil {
			return sandboxtools.CommandMetadata{}, fmt.Errorf("error running start command: %w", err)
		}

		return cmdMeta, nil
	}
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
