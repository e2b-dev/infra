package build

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/builder"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/templateconfig"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type TemplateBuilder struct {
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

	sbxTimeout           = time.Hour
	provisionTimeout     = 5 * time.Minute
	configurationTimeout = 5 * time.Minute
	waitEnvdTimeout      = 60 * time.Second
)

var defaultUser = "root"

type buildMetadata struct {
	envVars map[string]string
	user    string
	workdir *string
}

type TemplateMetadata struct {
	TemplateID string
	BuildID    string
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
) *TemplateBuilder {
	return &TemplateBuilder{
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
// 5. Run two additional commands:
//   - configuration script (enable swap, create user, change folder permissions, etc.)
//   - start command (if defined), together with the ready command (always with default value if not defined)
//
// 6. Snapshot
// 7. Upload template
func (b *TemplateBuilder) Build(ctx context.Context, template *templateconfig.TemplateConfig, engineConfig *templatemanager.EngineConfig) (r *Result, e error) {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	logsWriter := template.BuildLogsWriter
	postProcessor := writer.NewPostProcessor(ctx, logsWriter)
	go postProcessor.Start()
	defer func() {
		postProcessor.Stop(e)
	}()

	// defer func() {
	// 	// Remove build files if build fails or times out
	// 	removeErr := b.templateStorage.Remove(ctx, snapshotTemplateFiles.BuildId)
	// 	if removeErr != nil {
	// 		uploadErr = errors.Join(uploadErr, fmt.Errorf("error removing build files: %w", removeErr))
	// 	}
	// }()

	envdVersion, err := GetEnvdVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting envd version: %w", err)
	}

	envdHash, err := GetEnvdHash(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting envd binary hash: %w", err)
	}

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

	finalTemplate := TemplateMetadata{
		TemplateID: template.TemplateID,
		BuildID:    template.BuildID,
	}
	// Stable hash of base
	baseSHA := sha256.New()
	baseSHA.Write([]byte(template.FromImage))
	baseSHA.Write([]byte(envdHash))
	baseSHA.Write([]byte(provisionScriptFile))
	baseSHA.Write([]byte(string(template.DiskSizeMB)))
	baseHash := fmt.Sprintf("%x", baseSHA.Sum(nil))

	baseTemplate := getTemplateFromHash(ctx, b.storage, finalTemplate.TemplateID, "", baseHash)
	// Invalidate base cache if the first step has Force set to true
	if len(template.Steps) > 0 && template.Steps[0].Force != nil && *template.Steps[0].Force {
		baseTemplate = TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.NewString(),
		}
	}

	lastTemplate := baseTemplate
	template.TemplateID = baseTemplate.TemplateID
	template.BuildID = baseTemplate.BuildID
	defer func() {
		template.BuildID = finalTemplate.BuildID
		template.TemplateID = finalTemplate.TemplateID
	}()

	// Set force for all steps after first step
	shouldRebuild := false
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

	baseSandboxConfig := template.ToSandboxConfig(envdVersion)

	lastCached := false
	baseCached, err := isCached(ctx, b.storage, baseTemplate.BuildID)
	if err != nil {
		return nil, fmt.Errorf("error checking if base layer is cached: %w", err)
	}
	if baseCached {
		lastCached = true
		postProcessor.WriteMsg("Base layer is cached, skipping build")
	} else {
		templateFiles := storage.NewTemplateFiles(
			baseTemplate.TemplateID,
			baseTemplate.BuildID,
			template.KernelVersion,
			template.FirecrackerVersion,
		)
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

		rootfs, memfile, envsImg, err := Build(
			ctx,
			b.tracer,
			template,
			engineConfig,
			postProcessor,
			b.artifactRegistry,
			b.storage,
			b.networkPool,
			b.templateCache,
			b.devicePool,
			templateBuildDir,
			rootfsPath,
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

		err = b.provisionSandbox(ctx, postProcessor, template, envdVersion, localTemplate, rootfsProvisionPath)
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
			sbxTimeout,
			rootfsPath,
			fc.ProcessOptions{
				InitScriptPath:      systemdInitPath,
				KernelLogs:          env.IsDevelopment(),
				SystemdToKernelLogs: false,
			},
			config.AllowSandboxInternet,
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

		err = b.pauseAndUpload(
			ctx,
			b.tracer,
			postProcessor,
			b.storage,
			uploadErrGroup,
			sourceSbx,
			finalTemplate.TemplateID,
			"",
			baseHash,
			sourceSbx.Config.TemplateId,
			sourceSbx.Config.BuildId,
		)
		if err != nil {
			return nil, fmt.Errorf("error pausing and uploading template: %w", err)
		}
	}

	// TMP
	for i, step := range template.Steps {
		layerIndex := i + 1
		force := step.Force != nil && *step.Force
		cmd := fmt.Sprintf("%s %s", strings.ToUpper(step.Type), strings.Join(step.Args, " "))

		// Generate a new template ID and build ID for the step
		newTemplate := TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.NewString(),
		}
		if !force {
			// Fetch stable uuid from the step hash
			newTemplate = getTemplateFromHash(ctx, b.storage, finalTemplate.TemplateID, baseHash, step.Hash)
		}

		// Apply changes like env vars or workdir locally only, no need to run in sandbox
		// These changes are not cached and run every time
		fullyProcessed, err := b.applyLocalCommand(ctx, step, buildMetadata)
		if err != nil {
			return nil, fmt.Errorf("error applying command: %w", err)
		}

		// Check if the layer is cached
		found, err := isCached(ctx, b.storage, newTemplate.BuildID)
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

		// Run commands in sandbox only if not cached
		if !isCached {
			err = b.runInSandbox(
				ctx,
				postProcessor,
				uploadErrGroup,
				baseSandboxConfig,
				finalTemplate.TemplateID,
				baseHash,
				step.Hash,
				lastTemplate,
				newTemplate,
				true,
				func(ctx context.Context, sbx *sandbox.Sandbox) error {
					err := b.applyCommand(ctx, postProcessor, finalTemplate.TemplateID, sbx, prefix, step, buildMetadata)
					if err != nil {
						return fmt.Errorf("error processing layer: %w", err)
					}

					// Sync FS changes to the FS after exectution
					err = b.runCommand(
						ctx,
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
				return nil, fmt.Errorf("error running configuration script in sandbox: %w", err)
			}
		}

		lastTemplate = newTemplate
	}
	// TMP

	// Run post-processing actions in the sandbox
	err = b.runInSandbox(
		ctx,
		postProcessor,
		uploadErrGroup,
		baseSandboxConfig,
		finalTemplate.TemplateID,
		"config-run-cmd",
		baseHash,
		lastTemplate,
		finalTemplate,
		false,
		func(ctx context.Context, sbx *sandbox.Sandbox) error {
			// Run configuration script
			var scriptDef bytes.Buffer
			err = ConfigureScriptTemplate.Execute(&scriptDef, map[string]string{})
			if err != nil {
				return fmt.Errorf("error executing provision script: %w", err)
			}

			configCtx, configCancel := context.WithTimeout(ctx, configurationTimeout)
			defer configCancel()
			err = b.runCommand(
				configCtx,
				postProcessor,
				"config",
				sbx.Metadata.Config.SandboxId,
				scriptDef.String(),
				"root",
				nil,
				map[string]string{},
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
					cwd := "/home/user"
					err := b.runCommandWithConfirmation(
						commandsCtx,
						postProcessor,
						"start",
						sbx.Metadata.Config.SandboxId,
						template.StartCmd,
						"root",
						&cwd,
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
				template,
				sbx.Metadata.Config.SandboxId,
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

func (b *TemplateBuilder) provisionSandbox(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	template *templateconfig.TemplateConfig,
	envdVersion string,
	localTemplate *sbxtemplate.LocalTemplate,
	rootfsPath string,
) (e error) {
	ctx, childSpan := b.tracer.Start(ctx, "provision-sandbox")
	defer childSpan.End()

	logsWriter := &writer.PrefixFilteredWriter{Writer: postProcessor, PrefixFilter: logExternalPrefix}
	defer logsWriter.Close()

	sbx, cleanup, err := sandbox.CreateSandbox(
		ctx,
		b.tracer,
		b.networkPool,
		b.devicePool,
		template.ToSandboxConfig(envdVersion),
		localTemplate,
		provisionTimeout,
		rootfsPath,
		fc.ProcessOptions{
			InitScriptPath: busyBoxInitPath,
			// Always show kernel logs during the provisioning phase,
			// the sandbox is then started with systemd and without kernel logs.
			KernelLogs: true,

			// Show provision script logs to the user
			Stdout: logsWriter,
			Stderr: logsWriter,
		},
		// Allow sandbox internet access during provisioning
		true,
	)
	defer func() {
		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			e = fmt.Errorf("error cleaning up sandbox: %w", cleanupErr)
		}
	}()
	if err != nil {
		return fmt.Errorf("error creating sandbox: %w", err)
	}
	err = sbx.WaitForExit(
		ctx,
		b.tracer,
	)
	if err != nil {
		return fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	// Verify the provisioning script exit status
	exitStatus, err := ext4.ReadFile(ctx, b.tracer, rootfsPath, provisionScriptResultPath)
	if err != nil {
		return fmt.Errorf("error reading provision result: %w", err)
	}
	defer ext4.RemoveFile(ctx, b.tracer, rootfsPath, provisionScriptResultPath)

	// Fallback to "1" if the file is empty or not found
	if exitStatus == "" {
		exitStatus = "1"
	}
	if exitStatus != "0" {
		return fmt.Errorf("provision script failed with exit status: %s", exitStatus)
	}

	return nil
}

func (b *TemplateBuilder) enlargeDiskAfterProvisioning(
	ctx context.Context,
	template *templateconfig.TemplateConfig,
	rootfsPath string,
) error {
	// Resize rootfs to accommodate for the provisioning script size change
	rootfsFreeSpace, err := ext4.GetFreeSpace(ctx, b.tracer, rootfsPath, template.RootfsBlockSize())
	if err != nil {
		return fmt.Errorf("error getting free space: %w", err)
	}
	sizeDiff := template.DiskSizeMB<<ToMBShift - rootfsFreeSpace
	zap.L().Debug("adding provision size diff to rootfs",
		zap.Int64("size_add", sizeDiff),
		zap.Int64("size_free", rootfsFreeSpace),
		zap.Int64("size_target", template.DiskSizeMB<<ToMBShift),
	)
	if sizeDiff <= 0 {
		zap.L().Debug("no need to enlarge rootfs, skipping")
		return nil
	}
	rootfsFinalSize, err := ext4.Enlarge(ctx, b.tracer, rootfsPath, sizeDiff)
	if err != nil {
		// Debug filesystem stats on error
		cmd := exec.Command("tune2fs", "-l", rootfsPath)
		output, dErr := cmd.Output()
		zap.L().Error(string(output), zap.Error(dErr))

		return fmt.Errorf("error enlarging rootfs: %w", err)
	}
	template.RootfsSize = rootfsFinalSize

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, false)
	if err != nil {
		zap.L().Error("final enlarge filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)

		// Occasionally there are Block bitmap differences. For this reason, we retry with fix.
		ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
		zap.L().Error("final enlarge filesystem ext4 integrity - retry with fix",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		if err != nil {
			return fmt.Errorf("error checking final enlarge filesystem integrity: %w", err)
		}
	} else {
		zap.L().Debug("final enlarge filesystem ext4 integrity",
			zap.String("result", ext4Check),
		)
	}
	return nil
}

func (b *TemplateBuilder) runInSandbox(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	uploadErrGroup *errgroup.Group,
	sourceSbxConfig *orchestrator.SandboxConfig,
	finalTemplateID string,
	baseHash,
	hash string,
	sourceTemplate TemplateMetadata,
	exportTemplate TemplateMetadata,
	resumeSandbox bool,
	fun func(ctx context.Context, sbx *sandbox.Sandbox) error,
) error {
	ctx, childSpan := b.tracer.Start(ctx, "run-in-sandbox")
	defer childSpan.End()

	postProcessor.WriteMsg(fmt.Sprintf("Running action in: %s/%s", sourceTemplate.TemplateID, sourceTemplate.BuildID))

	// Resume sandbox source files/config
	sbxConfig := &orchestrator.SandboxConfig{
		TemplateId:         sourceTemplate.TemplateID,
		BuildId:            sourceTemplate.BuildID,
		ExecutionId:        uuid.NewString(),
		KernelVersion:      sourceSbxConfig.KernelVersion,
		FirecrackerVersion: sourceSbxConfig.FirecrackerVersion,
		HugePages:          sourceSbxConfig.HugePages,
		SandboxId:          sourceSbxConfig.SandboxId,
		EnvdVersion:        sourceSbxConfig.EnvdVersion,
		Vcpu:               sourceSbxConfig.Vcpu,
		RamMb:              sourceSbxConfig.RamMb,

		BaseTemplateId: sourceSbxConfig.BaseTemplateId,
	}

	var sbx *sandbox.Sandbox
	var cleanupRes *sandbox.Cleanup
	var err error
	if resumeSandbox {
		postProcessor.WriteMsg("Resuming sandbox")
		sbx, cleanupRes, err = sandbox.ResumeSandbox(
			ctx,
			b.tracer,
			b.networkPool,
			b.templateCache,
			sbxConfig,
			uuid.New().String(),
			time.Now(),
			time.Now().Add(time.Minute),
			b.devicePool,
			config.AllowSandboxInternet,
			false,
		)
	} else {
		postProcessor.WriteMsg("Creating new sandbox")
		var localTemplate sbxtemplate.Template

		// Sandbox source files
		localTemplate, err = b.templateCache.GetTemplate(
			sbxConfig.TemplateId,
			sbxConfig.BuildId,
			sbxConfig.KernelVersion,
			sbxConfig.FirecrackerVersion,
		)
		if err != nil {
			return fmt.Errorf("failed to get template snapshot data: %w", err)
		}

		oldMemfile, err := localTemplate.Memfile()
		if err != nil {
			return fmt.Errorf("error getting memfile from local template: %w", err)
		}

		// Create new memfile with the size of the sandbox RAM, this updates the underlying memfile.
		// This is ok as the sandbox is started from the beginning.
		memfile, err := block.NewEmpty(sbxConfig.RamMb<<ToMBShift, oldMemfile.BlockSize(), uuid.MustParse(sbxConfig.BuildId))
		if err != nil {
			return fmt.Errorf("error creating memfile: %w", err)
		}
		err = localTemplate.ReplaceMemfile(memfile)
		if err != nil {
			return fmt.Errorf("error setting memfile for local template: %w", err)
		}

		// New sandbox config
		sbxConfig.TemplateId = exportTemplate.TemplateID
		sbxConfig.BuildId = exportTemplate.BuildID
		sbxConfig.BaseTemplateId = exportTemplate.TemplateID
		sbx, cleanupRes, err = sandbox.CreateSandbox(
			ctx,
			b.tracer,
			b.networkPool,
			b.devicePool,
			sbxConfig,
			localTemplate,
			sbxTimeout,
			"",
			fc.ProcessOptions{
				InitScriptPath:      systemdInitPath,
				KernelLogs:          env.IsDevelopment(),
				SystemdToKernelLogs: false,
			},
			config.AllowSandboxInternet,
		)
	}
	defer func() {
		cleanupErr := cleanupRes.Run(ctx)
		if cleanupErr != nil {
			b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
		}
	}()
	if err != nil {
		return fmt.Errorf("error resuming sandbox: %w", err)
	}
	if !resumeSandbox {
		err = sbx.WaitForEnvd(
			ctx,
			b.tracer,
			waitEnvdTimeout,
		)
		if err != nil {
			return fmt.Errorf("failed to wait for sandbox start: %w", err)
		}
	}

	// Add to proxy so we can call envd commands
	b.sandboxes.Insert(sbx.Metadata.Config.SandboxId, sbx)
	defer func() {
		b.sandboxes.Remove(sbx.Metadata.Config.SandboxId)
		b.proxy.RemoveFromPool(sbx.Metadata.Config.ExecutionId)
	}()

	err = fun(ctx, sbx)
	if err != nil {
		return fmt.Errorf("error running action in sandbox: %w", err)
	}

	err = b.pauseAndUpload(
		ctx,
		b.tracer,
		postProcessor,
		b.storage,
		uploadErrGroup,
		sbx,
		finalTemplateID,
		baseHash,
		hash,
		exportTemplate.TemplateID,
		exportTemplate.BuildID,
	)
	if err != nil {
		return fmt.Errorf("error pausing and uploading template: %w", err)
	}

	return nil
}

func (b *TemplateBuilder) pauseAndUpload(
	ctx context.Context,
	tracer trace.Tracer,
	postProcessor *writer.PostProcessor,
	persistance storage.StorageProvider,
	uploadErrGroup *errgroup.Group,
	sbx *sandbox.Sandbox,
	finalTemplateID string,
	baseHash,
	hash string,
	templateID string,
	buildID string,
) error {
	ctx, childSpan := tracer.Start(ctx, "pause-and-upload")
	defer childSpan.End()

	// Pause sandbox
	snapshotTemplateFiles := storage.NewTemplateFiles(
		templateID,
		buildID,
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
	)
	postProcessor.WriteMsg(fmt.Sprintf("Caching template layer: %s/%s", snapshotTemplateFiles.TemplateId, buildID))

	snapshotTemplateCacheFiles, err := snapshotTemplateFiles.NewTemplateCacheFiles()
	if err != nil {
		return fmt.Errorf("error creating template files: %w", err)
	}
	snapshot, err := sbx.Pause(
		ctx,
		tracer,
		snapshotTemplateCacheFiles,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}
	// TODO: Do proper cleanup of the snapshot
	// defer snapshot.Close(ctx)

	// Add snapshot to template cache so it can be used immediately
	err = b.templateCache.AddSnapshot(
		snapshotTemplateFiles.TemplateId,
		snapshotTemplateFiles.BuildId,
		snapshotTemplateFiles.KernelVersion,
		snapshotTemplateFiles.FirecrackerVersion,
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		snapshot.Snapfile,
		snapshot.MemfileDiff,
		snapshot.RootfsDiff,
	)

	// Upload snapshot async, it's added to the template cache immediately
	uploadErrGroup.Go(func() error {
		postProcessor.WriteMsg(fmt.Sprintf("Uploading template layer: %s/%s", snapshotTemplateFiles.TemplateId, snapshotTemplateFiles.BuildId))
		err := snapshot.Upload(
			ctx,
			persistance,
			snapshotTemplateFiles,
		)
		if err != nil {
			return fmt.Errorf("error uploading snapshot: %w", err)
		}

		err = saveTemplateToHash(ctx, persistance, finalTemplateID, baseHash, hash, TemplateMetadata{
			TemplateID: snapshotTemplateFiles.TemplateId,
			BuildID:    snapshotTemplateFiles.BuildId,
		})
		if err != nil {
			return fmt.Errorf("error saving UUID to hash mapping: %w", err)
		}

		postProcessor.WriteMsg(fmt.Sprintf("Template layer saved: %s/%s", snapshotTemplateFiles.TemplateId, snapshotTemplateFiles.BuildId))
		return nil
	})

	return nil
}

func (b *TemplateBuilder) applyLocalCommand(
	ctx context.Context,
	step *templatemanager.TemplateStep,
	buildMetadata *buildMetadata,
) (bool, error) {
	ctx, span := b.tracer.Start(ctx, "apply-command-local", trace.WithAttributes(
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.hash", step.Hash),
		attribute.String("step.files.hash", Sprintp(step.FilesHash)),
		attribute.String("metadata.user", buildMetadata.user),
		attribute.String("metadata.workdir", Sprintp(buildMetadata.workdir)),
		attribute.String("metadata.env_vars", fmt.Sprintf("%v", buildMetadata.envVars)),
	))
	defer span.End()

	cmdType := strings.ToUpper(step.Type)
	args := step.Args

	switch cmdType {
	case "ARG":
		// args: [key value]
		if len(args) < 2 {
			return false, fmt.Errorf("ARG requires a key and value argument")
		}
		buildMetadata.envVars[args[0]] = args[1]
		return true, nil
	case "ENV":
		// args: [key value]
		if len(args) < 2 {
			return false, fmt.Errorf("ENV requires a key and value argument")
		}
		buildMetadata.envVars[args[0]] = args[1]
		return true, nil
	case "WORKDIR":
		// args: [path]
		if len(args) < 1 {
			return false, fmt.Errorf("WORKDIR requires a path argument")
		}
		cwd := args[0]
		buildMetadata.workdir = &cwd
		return false, nil
	case "USER":
		// args: [username]
		if len(args) < 1 {
			return false, fmt.Errorf("USER requires a username argument")
		}
		buildMetadata.user = args[0]
		return false, nil
	default:
		return false, nil
	}
}

func (b *TemplateBuilder) applyCommand(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	templateID string,
	sbx *sandbox.Sandbox,
	prefix string,
	step *templatemanager.TemplateStep,
	buildMetadata *buildMetadata,
) error {
	ctx, span := b.tracer.Start(ctx, "apply-command", trace.WithAttributes(
		attribute.String("prefix", prefix),
		attribute.String("sandbox.id", sbx.Metadata.Config.SandboxId),
		attribute.String("step.type", step.Type),
		attribute.StringSlice("step.args", step.Args),
		attribute.String("step.hash", step.Hash),
		attribute.String("step.files.hash", Sprintp(step.FilesHash)),
		attribute.String("metadata.user", buildMetadata.user),
		attribute.String("metadata.workdir", Sprintp(buildMetadata.workdir)),
		attribute.String("metadata.env_vars", fmt.Sprintf("%v", buildMetadata.envVars)),
	))
	defer span.End()

	cmdType := strings.ToUpper(step.Type)
	args := step.Args

	switch cmdType {
	case "ADD":
		// args: [localPath containerPath]
		fallthrough
	case "COPY":
		// args: [localPath containerPath]
		if len(args) < 2 {
			return fmt.Errorf("%s requires a local path and a container path argument", cmdType)
		}

		if step.FilesHash == nil || *step.FilesHash == "" {
			return fmt.Errorf("%s requires files hash to be set", cmdType)
		}

		obj, err := b.storage.OpenObject(ctx, builder.GetLayerFilesCachePath(templateID, *step.FilesHash))
		if err != nil {
			return fmt.Errorf("failed to open files object from storage: %w", err)
		}

		pr, pw := io.Pipe()
		// Start writing tar data to the pipe writer in a goroutine
		go func() {
			defer pw.Close()
			if _, err := obj.WriteTo(pw); err != nil {
				pw.CloseWithError(err)
			}
		}()

		tmpFile, err := os.CreateTemp("", "layer-file-*.tar")
		if err != nil {
			return fmt.Errorf("failed to create temporary file for layer tar: %w", err)
		}
		defer os.Remove(tmpFile.Name())
		defer tmpFile.Close()

		_, err = io.Copy(tmpFile, pr)
		if err != nil {
			return fmt.Errorf("failed to copy layer tar data to temporary file: %w", err)
		}

		// TODO: Cleanup the temporary file from the sandbox
		sbxTargetPath := fmt.Sprintf("/tmp/%s.tar", *step.FilesHash)
		err = b.copyFile(ctx, postProcessor, sbx.Metadata.Config.SandboxId, buildMetadata.user, tmpFile.Name(), sbxTargetPath)
		if err != nil {
			return fmt.Errorf("failed to copy layer tar data to sandbox: %w", err)
		}

		return b.runCommand(
			ctx,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`tar -xzvf "%s" -C "%s" --strip-components=1`, sbxTargetPath, args[1]),
			buildMetadata.user,
			buildMetadata.workdir,
			buildMetadata.envVars,
		)
	case "RUN":
		// args: command and args, e.g., ["sh", "-c", "echo hi"]
		if len(args) < 1 {
			return fmt.Errorf("RUN requires command arguments")
		}

		cmd := strings.Join(args, " ")
		return b.runCommand(
			ctx,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			cmd,
			buildMetadata.user,
			buildMetadata.workdir,
			buildMetadata.envVars,
		)
	case "USER":
		// args: [username]
		if len(args) < 1 {
			return fmt.Errorf("USER requires a username argument")
		}

		return b.runCommand(
			ctx,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			"adduser "+args[0],
			"root",
			nil,
			buildMetadata.envVars,
		)
	case "WORKDIR":
		// args: [path]
		if len(args) < 1 {
			return fmt.Errorf("WORKDIR requires a path argument")
		}

		return b.runCommand(
			ctx,
			postProcessor,
			prefix,
			sbx.Metadata.Config.SandboxId,
			fmt.Sprintf(`mkdir -p "%s"`, Sprintp(buildMetadata.workdir)),
			buildMetadata.user,
			nil,
			buildMetadata.envVars,
		)
	default:
		return fmt.Errorf("unsupported command type: %s", cmdType)
	}
}

func isCached(
	ctx context.Context,
	s storage.StorageProvider,
	buildID string,
) (bool, error) {
	obj, err := s.OpenObject(ctx, fmt.Sprintf("%s/rootfs.ext4.header", buildID))
	if err != nil {
		return false, fmt.Errorf("error checking if layer is cached: %w", err)
	}
	_, err = obj.Size()
	if err == nil {
		return true, nil
	}

	return false, nil
}

func getTemplateFromHash(ctx context.Context, s storage.StorageProvider, templateID string, baseHash string, hash string) TemplateMetadata {
	obj, err := s.OpenObject(ctx, uuidToHashPath(templateID, baseHash, hash))
	if err != nil {
		return TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.New().String(),
		}
	}

	var buf bytes.Buffer
	_, err = obj.WriteTo(&buf)
	if err != nil {
		return TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.New().String(),
		}
	}

	var templateMetadata TemplateMetadata
	err = json.Unmarshal(buf.Bytes(), &templateMetadata)
	if err != nil {
		zap.L().Error("error unmarshalling template metadata from hash", zap.Error(err))
		return TemplateMetadata{
			TemplateID: id.Generate(),
			BuildID:    uuid.New().String(),
		}
	}

	return templateMetadata
}

func saveTemplateToHash(ctx context.Context, s storage.StorageProvider, templateID string, baseHash, hash string, template TemplateMetadata) error {
	obj, err := s.OpenObject(ctx, uuidToHashPath(templateID, baseHash, hash))
	if err != nil {
		return fmt.Errorf("error creating object for saving UUID: %w", err)
	}

	marshalled, err := json.Marshal(template)
	if err != nil {
		return fmt.Errorf("error marshalling template metadata: %w", err)
	}

	buf := bytes.NewBuffer(marshalled)
	_, err = obj.ReadFrom(buf)
	if err != nil {
		return fmt.Errorf("error writing UUID to object: %w", err)
	}

	return nil
}

func uuidToHashPath(templateID, baseHash, hash string) string {
	reSHA := sha256.New()
	reSHA.Write([]byte(baseHash))
	reSHA.Write([]byte(hash))
	reHash := fmt.Sprintf("%x", reSHA.Sum(nil))
	return fmt.Sprintf("builder/cache/%s/index/%s", templateID, reHash)
}

func Sprintp(s *string) string {
	if s == nil {
		return "<nil>"
	}

	return *s
}
