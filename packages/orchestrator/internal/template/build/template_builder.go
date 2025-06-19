package build

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	templatelocal "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
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
}

const (
	templatesDirectory = "/tmp/build-templates"

	sbxTimeout           = time.Hour
	provisionTimeout     = 5 * time.Minute
	configurationTimeout = 5 * time.Minute
	waitEnvdTimeout      = 60 * time.Second

	cleanupTimeout = time.Second * 10
)

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
func (b *TemplateBuilder) Build(ctx context.Context, template *TemplateConfig) (r *Result, e error) {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	logsWriter := template.BuildLogsWriter
	postProcessor := writer.NewPostProcessor(ctx, logsWriter)
	go postProcessor.Start()
	defer func() {
		postProcessor.Stop(e)
	}()

	envdVersion, err := GetEnvdVersion(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting envd version: %w", err)
	}

	templateCacheFiles, err := template.NewTemplateCacheFiles()
	if err != nil {
		return nil, fmt.Errorf("error creating template files: %w", err)
	}

	templateBuildDir := filepath.Join(templatesDirectory, template.BuildId)
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

	rootfs, memfile, buildConfig, err := Build(
		ctx,
		b.tracer,
		template,
		postProcessor,
		b.artifactRegistry,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		return nil, fmt.Errorf("error building environment: %w", err)
	}

	localTemplate := templatelocal.NewLocalTemplate(templateCacheFiles, rootfs, memfile)
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
	sbx, cleanup, err := sandbox.CreateSandbox(
		ctx,
		b.tracer,
		b.networkPool,
		b.devicePool,
		template.ToSandboxConfig(envdVersion),
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
	err = sbx.WaitForEnvd(
		ctx,
		b.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}
	// Add to proxy so we can call envd commands
	b.sandboxes.Insert(sbx.Metadata.Config.SandboxId, sbx)
	defer func() {
		b.sandboxes.Remove(sbx.Metadata.Config.SandboxId)
		b.proxy.RemoveFromPool(sbx.Metadata.Config.ExecutionId)
	}()

	// Run configuration script
	var scriptDef bytes.Buffer
	err = ConfigureScriptTemplate.Execute(&scriptDef, map[string]string{})
	if err != nil {
		return nil, fmt.Errorf("error executing provision script: %w", err)
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
		return nil, fmt.Errorf("error running configuration script: %w", err)
	}

	// Env variables for the start command and ready command
	envVars := oci.ParseEnvs(buildConfig.Env)

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
				envVars,
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
		envVars,
	)
	if err != nil {
		return nil, fmt.Errorf("error running ready command: %w", err)
	}

	// Wait for the start command to start executing.
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("error waiting for start command: %w", commandsCtx.Err())
	case <-startCmdConfirm:
	}
	// Cancel the start command context (it's running in the background anyway).
	// If it has already finished, check the error.
	commandsCancel()
	err = startCmd.Wait()
	if err != nil {
		return nil, fmt.Errorf("error running start command: %w", err)
	}

	// Pause sandbox
	postProcessor.WriteMsg("Pausing sandbox template")
	snapshot, err := sbx.Pause(
		ctx,
		b.tracer,
		templateCacheFiles,
	)
	if err != nil {
		return nil, fmt.Errorf("error processing vm: %w", err)
	}

	// Upload
	postProcessor.WriteMsg("Uploading template")
	uploadErrCh := b.uploadTemplate(
		ctx,
		template.TemplateFiles,
		snapshot,
	)

	uploadErr := <-uploadErrCh
	if uploadErr != nil {
		return nil, fmt.Errorf("error uploading template: %w", uploadErr)
	}

	return &Result{
		EnvdVersion:  envdVersion,
		RootfsSizeMB: template.RootfsSizeMB(),
	}, nil
}

func (b *TemplateBuilder) uploadTemplate(
	ctx context.Context,
	templateFiles *storage.TemplateFiles,
	snapshot *sandbox.Snapshot,
) chan error {
	errCh := make(chan error, 1)

	go func() {
		// Remove build files if build fails or times out
		var err error
		defer func() {
			if err != nil {
				removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
				defer cancel()

				removeErr := b.templateStorage.Remove(removeCtx, templateFiles.BuildId)
				if removeErr != nil {
					telemetry.ReportError(ctx, "error while removing build files", removeErr)
				}
			}
		}()
		defer func() {
			err := snapshot.Close(ctx)
			if err != nil {
				zap.L().Error("error closing snapshot", zap.Error(err), logger.WithBuildID(templateFiles.BuildId), logger.WithTemplateID(templateFiles.TemplateId))
			}
		}()
		defer close(errCh)

		templateBuild := storage.NewTemplateBuild(
			snapshot.MemfileDiffHeader,
			snapshot.RootfsDiffHeader,
			b.storage,
			templateFiles,
		)

		memfileDiffPath, err := snapshot.MemfileDiff.CachePath()
		if err != nil {
			errCh <- fmt.Errorf("error getting memfile diff path: %w", err)
			return
		}

		rootfsDiffPath, err := snapshot.RootfsDiff.CachePath()
		if err != nil {
			errCh <- fmt.Errorf("error getting rootfs diff path: %w", err)
			return
		}

		snapfilePath := snapshot.Snapfile.Path()

		uploadErrCh := templateBuild.Upload(
			ctx,
			snapfilePath,
			&memfileDiffPath,
			&rootfsDiffPath,
		)

		// Wait for the upload to finish
		err = <-uploadErrCh
		if err != nil {
			errCh <- fmt.Errorf("error uploading template build: %w", err)
			return
		}

		errCh <- nil
	}()

	return errCh
}

func (b *TemplateBuilder) provisionSandbox(
	ctx context.Context,
	postProcessor *writer.PostProcessor,
	template *TemplateConfig,
	envdVersion string,
	localTemplate *templatelocal.LocalTemplate,
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
	template *TemplateConfig,
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
	template.rootfsSize = rootfsFinalSize

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, false)
	if err != nil {
		zap.L().Error("final enlarge filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)

		// Occasionally there is Block bitmap differences. For this reason, we retry with fix.
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
