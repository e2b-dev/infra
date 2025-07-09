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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type Builder struct {
	logger *zap.Logger
	tracer trace.Tracer

	storage          storage.StorageProvider
	devicePool       *nbd.DevicePool
	networkPool      *network.Pool
	buildLogger      *zap.Logger
	templateStorage  *template.Storage
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

type buildMetadata struct {
	envVars map[string]string
	user    string
	workdir *string
}

func NewBuilder(
	logger *zap.Logger,
	buildLogger *zap.Logger,
	tracer trace.Tracer,
	templateStorage *template.Storage,
	storage storage.StorageProvider,
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
		storage:          storage,
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
func (b *Builder) Build(ctx context.Context, metadata config.TemplateMetadata, template *config.TemplateConfig, logsWriter io.Writer) (r *Result, e error) {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	// Validate template, update force layers if needed
	forceSteps(template)

	postProcessor := writer.NewPostProcessor(ctx, logsWriter)
	go postProcessor.Start()
	defer func() {
		postProcessor.Stop(ctx, e)
	}()

	defer func() {
		// Remove build files if build fails or times out
		removeErr := b.templateStorage.Remove(ctx, metadata.BuildID)
		if removeErr != nil {
			e = errors.Join(e, fmt.Errorf("error removing build files: %w", removeErr))
		}
	}()

	envdVersion, err := envd.GetEnvdVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting envd version: %w", err)
	}

	// Wait for all template layers to be uploaded
	uploadErrGroup, _ := errgroup.WithContext(ctx)
	defer func() {
		err := uploadErrGroup.Wait()
		if err != nil {
			e = errors.Join(e, fmt.Errorf("error uploading template layers: %w", err))
		}
	}()

	buildMetadata := &buildMetadata{
		envVars: make(map[string]string),
		user:    defaultUser,
		workdir: nil,
	}

	// This is a compability for old template builds
	if template.FromImage == "" {
		cwd := "/home/user"
		buildMetadata.workdir = &cwd
	}

	baseHash, err := getBaseHash(ctx, template)
	if err != nil {
		return nil, fmt.Errorf("error getting base hash: %w", err)
	}

	hash := template.FromImage
	baseTemplate := getTemplateFromHash(ctx, b.storage, metadata.TemplateID, baseHash, hash)
	// Invalidate base cache
	if template.Force != nil && *template.Force {
		baseTemplate = config.TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.NewString(),
		}
	}
	lastTemplate := baseTemplate

	baseSandboxConfig := template.ToSandboxConfig(baseTemplate, envdVersion)

	lastCached := false
	templateFiles := storage.NewTemplateFiles(
		baseTemplate.TemplateID,
		baseTemplate.BuildID,
		template.KernelVersion,
		template.FirecrackerVersion,
	)
	baseCached, err := isCached(ctx, b.storage, templateFiles)
	if err != nil {
		return nil, fmt.Errorf("error checking if base layer is cached: %w", err)
	}
	if baseCached {
		lastCached = true
		postProcessor.WriteMsg("Base layer is cached, skipping build")
	} else {
		templateCacheFiles, err := templateFiles.NewTemplateCacheFiles()
		if err != nil {
			return nil, fmt.Errorf("error creating template files: %w", err)
		}

		templateBuildDir := filepath.Join(templatesDirectory, baseTemplate.BuildID)
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

		provisionScript, err := getProvisionScript(ctx, ProvisionScriptParams{
			ResultPath: provisionScriptResultPath,
		})
		if err != nil {
			return nil, fmt.Errorf("error getting provision script: %w", err)
		}

		rootfs, memfile, envsImg, err := ConstructBaseTemplateFiles(
			ctx,
			b.tracer,
			metadata,
			baseTemplate.BuildID,
			template,
			postProcessor,
			b.artifactRegistry,
			b.storage,
			b.networkPool,
			b.templateCache,
			b.devicePool,
			templateBuildDir,
			rootfsPath,
			provisionScript,
			provisionLogPrefix,
		)
		if err != nil {
			return nil, fmt.Errorf("error building environment: %w", err)
		}

		// Env variables from the Docker image
		buildMetadata.envVars = oci.ParseEnvs(envsImg.Env)

		localTemplate := sbxtemplate.NewLocalTemplate(templateCacheFiles, rootfs, memfile)
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

		err = b.provisionSandbox(
			ctx,
			postProcessor,
			baseTemplate,
			template,
			envdVersion,
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

		err = b.enlargeDiskAfterProvisioning(ctx, template, rootfsPath)
		if err != nil {
			return nil, fmt.Errorf("error enlarging disk after provisioning: %w", err)
		}

		err = rootfs.UpdateSize()
		if err != nil {
			return nil, fmt.Errorf("error updating rootfs size: %w", err)
		}

		// Create sandbox for building template
		postProcessor.WriteMsg("Creating sandbox template")
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
			b.storage,
			b.templateCache,
			sourceSbx,
			metadata.TemplateID,
			baseHash,
			hash,
			baseTemplate.TemplateID,
			baseTemplate.BuildID,
		)
		if err != nil {
			return nil, fmt.Errorf("error pausing and uploading template: %w", err)
		}
	}

	// Build Steps
	for i, step := range template.Steps {
		layerIndex := i + 1
		force := step.Force != nil && *step.Force
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))

		// Generate a new template ID and build ID for the step
		newTemplate := config.TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.NewString(),
		}
		if !force {
			// Fetch stable uuid from the step hash
			newTemplate = getTemplateFromHash(ctx, b.storage, metadata.TemplateID, baseHash, step.Hash)
		}

		// Apply changes like env vars or workdir locally only, no need to run in sandbox
		// These changes are not cached and run every time
		fullyProcessed, err := b.applyLocalCommand(ctx, step, buildMetadata)
		if err != nil {
			return nil, fmt.Errorf("error applying command: %w", err)
		}

		// Check if the layer is cached
		templateFiles = storage.NewTemplateFiles(
			newTemplate.TemplateID,
			newTemplate.BuildID,
			baseSandboxConfig.KernelVersion,
			baseSandboxConfig.FirecrackerVersion,
		)
		found, err := isCached(ctx, b.storage, templateFiles)
		if err != nil {
			return nil, fmt.Errorf("error checking if layer is cached: %w", err)
		}
		isCached := !force && (found || (lastCached && fullyProcessed))
		lastCached = isCached

		cached := ""
		if isCached {
			cached = "CACHED "
		}
		prefix := fmt.Sprintf("builder %d/%d", layerIndex, len(template.Steps))
		postProcessor.WriteMsg(fmt.Sprintf("%s[%s] %s [%s]", cached, prefix, cmd, step.Hash))

		if fullyProcessed {
			// lastBuildID is not updated here because no new sandbox is run
			continue
		}

		// Run commands in the sandbox only if not cached
		if !isCached {
			err = b.buildLayer(
				ctx,
				postProcessor,
				uploadErrGroup,
				baseSandboxConfig,
				metadata.TemplateID,
				baseHash,
				step.Hash,
				lastTemplate,
				newTemplate,
				true,
				globalconfig.AllowSandboxInternet,
				func(ctx context.Context, sbx *sandbox.Sandbox) error {
					err := b.applyCommand(ctx, postProcessor, metadata.TemplateID, sbx, prefix, step, buildMetadata)
					if err != nil {
						return fmt.Errorf("error processing layer: %w", err)
					}

					// Sync FS changes to the FS after exectution
					err = sandboxtools.RunCommand(
						ctx,
						b.tracer,
						b.proxy,
						b.buildLogger,
						postProcessor,
						prefix,
						sbx.Metadata.Config.SandboxId,
						"sync",
						"root",
						nil,
						nil,
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

		lastTemplate = newTemplate
	}
	// Build Steps

	// Run post-processing actions in the sandbox
	err = b.buildLayer(
		ctx,
		postProcessor,
		uploadErrGroup,
		baseSandboxConfig,
		metadata.TemplateID,
		"config-run-cmd",
		baseHash,
		lastTemplate,
		metadata,
		false,
		globalconfig.AllowSandboxInternet,
		func(ctx context.Context, sbx *sandbox.Sandbox) error {
			// Run configuration script
			err := runConfiguration(
				ctx,
				b.tracer,
				b.proxy,
				b.buildLogger,
				postProcessor,
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
						buildMetadata.user,
						buildMetadata.workdir,
						buildMetadata.envVars,
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
				metadata,
				template,
				sbx.Metadata.Config.SandboxId,
				buildMetadata.user,
				buildMetadata.workdir,
				buildMetadata.envVars,
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
		},
	)
	if err != nil {
		return nil, fmt.Errorf("error running start and ready commands in sandbox: %w", err)
	}

	return &Result{
		EnvdVersion:  envdVersion,
		RootfsSizeMB: template.RootfsSizeMB(),
	}, nil
}

func isCached(
	ctx context.Context,
	s storage.StorageProvider,
	files *storage.TemplateFiles,
) (bool, error) {
	obj, err := s.OpenObject(ctx, files.StorageRootfsHeaderPath())
	if err != nil {
		return false, fmt.Errorf("error checking if layer is cached: %w", err)
	}
	_, err = obj.Size()
	if err == nil {
		return true, nil
	}

	return false, nil
}

// forceSteps sets force for all steps after the first encounter.
func forceSteps(template *config.TemplateConfig) {
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
}
