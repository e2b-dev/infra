package build

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"google.golang.org/protobuf/proto"

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
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
)

type Builder struct {
	logger *zap.Logger
	tracer trace.Tracer

	templateStorage  storage.StorageProvider
	buildStorage     storage.StorageProvider
	devicePool       *nbd.DevicePool
	networkPool      *network.Pool
	buildLogger      *zap.Logger
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
	buildLogger *zap.Logger,
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
		buildLogger:      buildLogger,
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
func (b *Builder) Build(ctx context.Context, finalMetadata storage.TemplateFiles, template config.TemplateConfig, logsWriter io.Writer) (r *Result, e error) {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	// Validate template, update force layers if needed
	template = forceSteps(template)

	isOldBuild := template.FromImage == ""

	postProcessor := writer.NewPostProcessor(ctx, logsWriter, isOldBuild)
	go postProcessor.Start()
	defer func() {
		postProcessor.Stop(ctx, e)
	}()

	postProcessor.WriteMsg(fmt.Sprintf("Building template %s/%s", finalMetadata.TemplateID, finalMetadata.BuildID))

	defer func() {
		if e == nil {
			return
		}
		// Remove build files if build fails
		removeErr := b.templateStorage.DeleteObjectsWithPrefix(context.Background(), finalMetadata.BuildID)
		if removeErr != nil {
			e = errors.Join(e, fmt.Errorf("error removing build files: %w", removeErr))
		}
	}()

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

	cmdMeta := sandboxtools.CommandMetadata{
		User:    defaultUser,
		WorkDir: nil,
		EnvVars: make(map[string]string),
	}

	// This is a compatibility for old template builds
	if isOldBuild {
		cwd := "/home/user"
		cmdMeta.WorkDir = &cwd
	}

	lastHash, err := hashBase(template)
	if err != nil {
		return nil, fmt.Errorf("error getting base hash: %w", err)
	}

	lastCached, baseMetadata, err := b.setupBase(ctx, finalMetadata, template, lastHash)
	if err != nil {
		return nil, fmt.Errorf("error setting up build: %w", err)
	}

	// Print the base layer information
	fromImage := template.FromImage
	if fromImage == "" {
		tag, err := b.artifactRegistry.GetTag(ctx, finalMetadata.TemplateID, finalMetadata.BuildID)
		if err != nil {
			return nil, fmt.Errorf("error getting tag for template: %w", err)
		}
		fromImage = tag
	}
	postProcessor.WriteMsg(layerInfo(lastCached, "base", "FROM "+fromImage, lastHash))

	// Build the base layer if not cached
	if !lastCached {
		templateBuildDir := filepath.Join(templatesDirectory, finalMetadata.BuildID)
		err = os.MkdirAll(templateBuildDir, 0o777)
		if err != nil {
			return nil, fmt.Errorf("error creating template build directory: %w", err)
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
			finalMetadata,
			baseMetadata.BuildID,
			template,
			postProcessor,
			b.artifactRegistry,
			templateBuildDir,
			rootfsPath,
		)
		if err != nil {
			return nil, fmt.Errorf("error building environment: %w", err)
		}

		// Env variables from the Docker image
		cmdMeta.EnvVars = oci.ParseEnvs(envsImg.Env)

		cacheFiles, err := baseMetadata.CacheFiles()
		if err != nil {
			return nil, fmt.Errorf("error creating template files: %w", err)
		}
		localTemplate := sbxtemplate.NewLocalTemplate(cacheFiles, rootfs, memfile)
		defer localTemplate.Close()

		// Provision sandbox with systemd and other vital parts
		postProcessor.WriteMsg("Provisioning sandbox template")
		// Just a symlink to the rootfs build file, so when the COW cache deletes the underlying file (here symlink),
		// it will not delete the rootfs file. We use the rootfs again later on to start the sandbox template.
		rootfsProvisionPath := filepath.Join(templateBuildDir, rootfsProvisionLink)
		err = os.Symlink(rootfsPath, rootfsProvisionPath)
		if err != nil {
			return nil, fmt.Errorf("error creating provision rootfs: %w", err)
		}

		baseSandboxConfig := &orchestrator.SandboxConfig{
			TemplateId:         baseMetadata.TemplateID,
			BuildId:            baseMetadata.BuildID,
			KernelVersion:      baseMetadata.KernelVersion,
			FirecrackerVersion: baseMetadata.FirecrackerVersion,

			BaseTemplateId: baseMetadata.TemplateID,

			Vcpu:        template.VCpuCount,
			RamMb:       template.MemoryMB,
			HugePages:   template.HugePages,
			EnvdVersion: envdVersion,
		}
		baseSandboxConfig.SandboxId = config.InstanceBuildPrefix + id.Generate()
		baseSandboxConfig.ExecutionId = uuid.NewString()
		err = b.provisionSandbox(
			ctx,
			postProcessor,
			baseSandboxConfig,
			localTemplate,
			rootfsProvisionPath,
			provisionScriptResultPath,
			provisionLogPrefix,
		)
		if err != nil {
			return nil, fmt.Errorf("error provisioning sandbox: %w", err)
		}

		// Check the rootfs filesystem corruption
		ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
		if err != nil {
			zap.L().Error("provisioned filesystem ext4 integrity",
				zap.String("result", ext4Check),
				zap.Error(err),
			)
			return nil, fmt.Errorf("error checking provisioned filesystem integrity: %w", err)
		}
		zap.L().Debug("provisioned filesystem ext4 integrity",
			zap.String("result", ext4Check),
		)

		rootfsSize, err := b.enlargeDiskAfterProvisioning(ctx, template, rootfsPath)
		if err != nil {
			return nil, fmt.Errorf("error enlarging disk after provisioning: %w", err)
		}

		err = rootfs.UpdateSize(rootfsSize)
		if err != nil {
			return nil, fmt.Errorf("error updating rootfs size: %w", err)
		}

		// Create sandbox for building template
		postProcessor.WriteMsg("Creating sandbox template")
		baseSandboxConfig = proto.Clone(baseSandboxConfig).(*orchestrator.SandboxConfig)
		baseSandboxConfig.SandboxId = config.InstanceBuildPrefix + id.Generate()
		baseSandboxConfig.ExecutionId = uuid.NewString()
		sourceSbx, cleanup, err := sandbox.CreateSandbox(
			ctx,
			b.tracer,
			b.networkPool,
			b.devicePool,
			baseSandboxConfig,
			localTemplate,
			baseLayerTimeout,
			rootfsPath,
			fc.ProcessOptions{
				InitScriptPath:      systemdInitPath,
				KernelLogs:          env.IsDevelopment(),
				SystemdToKernelLogs: false,
			},
			globalconfig.AllowSandboxInternet,
		)
		defer func() {
			cleanupErr := cleanup.Run(ctx)
			if cleanupErr != nil {
				b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
			}
		}()
		if err != nil {
			return nil, fmt.Errorf("error creating sandbox: %w", err)
		}
		err = sourceSbx.WaitForEnvd(
			ctx,
			b.tracer,
			waitEnvdTimeout,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to wait for sandbox start: %w", err)
		}

		err = pauseAndUpload(
			ctx,
			b.tracer,
			uploadErrGroup,
			postProcessor,
			b.templateStorage,
			b.buildStorage,
			b.templateCache,
			sourceSbx,
			finalMetadata.TemplateID,
			lastHash,
			baseMetadata,
		)
		if err != nil {
			return nil, fmt.Errorf("error pausing and uploading template: %w", err)
		}
	}

	sourceMetadata := baseMetadata

	// Build Steps
	for i, step := range template.Steps {
		layerIndex := i + 1
		lastHash = hashStep(lastHash, step)

		force := step.Force != nil && *step.Force

		// Generate a new template ID and build ID for the step
		stepMetadata := storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      sourceMetadata.KernelVersion,
			FirecrackerVersion: sourceMetadata.FirecrackerVersion,
		}
		if !force {
			// Fetch stable uuid from the step hash
			stepMetadata = templateMetaFromHash(ctx, b.buildStorage, sourceMetadata, finalMetadata.TemplateID, lastHash)
		}

		// Apply changes like env vars or workdir locally only, no need to run in sandbox
		// These changes are not cached and run every time
		fullyProcessed, err := b.applyLocalCommand(ctx, step, &cmdMeta)
		if err != nil {
			return nil, fmt.Errorf("error applying command: %w", err)
		}

		// Check if the layer is cached
		found, err := isCached(ctx, b.templateStorage, stepMetadata)
		if err != nil {
			return nil, fmt.Errorf("error checking if layer is cached: %w", err)
		}
		isCached := !force && (found || (lastCached && fullyProcessed))
		lastCached = isCached

		prefix := fmt.Sprintf("builder %d/%d", layerIndex, len(template.Steps))
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))
		postProcessor.WriteMsg(layerInfo(isCached, prefix, cmd, lastHash))

		if fullyProcessed {
			// sourceMetadata is not updated here because no new sandbox is run
			continue
		}

		// Run commands in the sandbox only if not cached
		if !isCached {
			err = b.buildLayer(
				ctx,
				postProcessor,
				uploadErrGroup,
				&orchestrator.SandboxConfig{
					BaseTemplateId: baseMetadata.TemplateID,

					// TODO: Here might be invalid data for the template resume, but they might not be used
					Vcpu:        template.VCpuCount,
					RamMb:       template.MemoryMB,
					HugePages:   template.HugePages,
					EnvdVersion: envdVersion,
				},
				finalMetadata.TemplateID,
				lastHash,
				sourceMetadata,
				stepMetadata,
				true,
				globalconfig.AllowSandboxInternet,
				func(ctx context.Context, sbx *sandbox.Sandbox) error {
					postProcessor.WriteMsg(fmt.Sprintf("Running action in: %s/%s", sourceMetadata.TemplateID, sourceMetadata.BuildID))

					err := b.applyCommand(ctx, postProcessor, finalMetadata.TemplateID, sbx, prefix, step, cmdMeta)
					if err != nil {
						return fmt.Errorf("error processing layer: %w", err)
					}

					// Sync FS changes to the FS after exectution
					err = sandboxtools.RunCommand(
						ctx,
						b.tracer,
						b.proxy,
						b.buildLogger,
						nil,
						prefix,
						sbx.Metadata.Config.SandboxId,
						"sync",
						sandboxtools.CommandMetadata{
							User: "root",
						},
					)
					if err != nil {
						return fmt.Errorf("error running sync command: %w", err)
					}

					return nil
				},
			)
			if err != nil {
				return nil, fmt.Errorf("error running build layer: %w", err)
			}
		}

		sourceMetadata = stepMetadata
	}
	// Build Steps

	// Run post-processing actions in the sandbox
	lastHash = hashKeys(lastHash, "config-run-cmd")
	err = b.buildLayer(
		ctx,
		postProcessor,
		uploadErrGroup,
		&orchestrator.SandboxConfig{
			Vcpu:        template.VCpuCount,
			RamMb:       template.MemoryMB,
			HugePages:   template.HugePages,
			EnvdVersion: envdVersion,
		},
		finalMetadata.TemplateID,
		lastHash,
		sourceMetadata,
		finalMetadata,
		false,
		globalconfig.AllowSandboxInternet,
		b.postProcessingFn(postProcessor, finalMetadata, template, cmdMeta),
	)
	if err != nil {
		return nil, fmt.Errorf("error running start and ready commands in sandbox: %w", err)
	}

	// Ensure the base layer is uploaded before getting the rootfs size
	err = uploadErrGroup.Wait()
	if err != nil {
		return nil, fmt.Errorf("error waiting for layers upload: %w", err)
	}

	// Get the base rootfs size from the template files
	// This is the size of the rootfs after provisioning and before building the layers
	// (as they don't change the rootfs size)
	rootfsSize, err := getRootfsSize(ctx, b.templateStorage, baseMetadata)
	if err != nil {
		return nil, fmt.Errorf("error getting rootfs size: %w", err)
	}

	return &Result{
		EnvdVersion:  envdVersion,
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
	metadata storage.TemplateFiles,
) (bool, error) {
	_, err := getRootfsSize(ctx, s, metadata)
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
	finalMetadata storage.TemplateFiles,
	template config.TemplateConfig,
	hash string,
) (bool, storage.TemplateFiles, error) {
	baseMetadata := templateMetaFromHash(ctx, b.buildStorage, finalMetadata, finalMetadata.TemplateID, hash)
	// Invalidate base cache
	if template.Force != nil && *template.Force {
		baseMetadata = storage.TemplateFiles{
			TemplateID:         id.Generate(),
			BuildID:            uuid.NewString(),
			KernelVersion:      finalMetadata.KernelVersion,
			FirecrackerVersion: finalMetadata.FirecrackerVersion,
		}
	}

	baseCached, err := isCached(ctx, b.templateStorage, baseMetadata)
	if err != nil {
		return false, storage.TemplateFiles{}, fmt.Errorf("error checking if base layer is cached: %w", err)
	}

	return baseCached, baseMetadata, nil
}

func (b *Builder) postProcessingFn(
	postProcessor *writer.PostProcessor,
	finalMetadata storage.TemplateFiles,
	template config.TemplateConfig,
	cmdMeta sandboxtools.CommandMetadata,
) func(ctx context.Context, sbx *sandbox.Sandbox) error {
	return func(ctx context.Context, sbx *sandbox.Sandbox) error {
		// Run configuration script
		err := runConfiguration(
			ctx,
			b.tracer,
			b.proxy,
			b.buildLogger,
			// TODO: Make this debug level
			nil,
			finalMetadata,
			sbx.Metadata.Config.SandboxId,
		)
		if err != nil {
			return fmt.Errorf("error running configuration script: %w", err)
		}

		// Start command
		commandsCtx, commandsCancel := context.WithCancel(ctx)
		defer commandsCancel()

		var startCmd errgroup.Group
		startCmdConfirm := make(chan struct{})
		if template.StartCmd != "" {
			postProcessor.WriteMsg("Running start command")
			startCmd.Go(func() error {
				err := sandboxtools.RunCommandWithConfirmation(
					commandsCtx,
					b.tracer,
					b.proxy,
					b.buildLogger,
					postProcessor,
					"start",
					sbx.Metadata.Config.SandboxId,
					template.StartCmd,
					cmdMeta,
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
		err = b.runReadyCommand(
			commandsCtx,
			postProcessor,
			// Use the final template here, because it contains the templateID for final build that is required for customer exceptions.
			finalMetadata,
			template,
			sbx.Metadata.Config.SandboxId,
			cmdMeta,
		)
		if err != nil {
			return fmt.Errorf("error running ready command: %w", err)
		}

		// Wait for the start command to start executing.
		select {
		case <-ctx.Done():
			return fmt.Errorf("error waiting for start command: %w", commandsCtx.Err())
		case <-startCmdConfirm:
		}
		// Cancel the start command context (it's running in the background anyway).
		// If it has already finished, check the error.
		commandsCancel()
		err = startCmd.Wait()
		if err != nil {
			return fmt.Errorf("error running start command: %w", err)
		}

		return nil
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
