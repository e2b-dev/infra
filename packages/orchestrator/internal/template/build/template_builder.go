package build

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	templatelocal "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateBuilder struct {
	logger *zap.Logger
	tracer trace.Tracer

	storage         storage.StorageProvider
	devicePool      *nbd.DevicePool
	networkPool     *network.Pool
	buildCache      *cache.BuildCache
	buildLogger     *zap.Logger
	templateStorage *template.Storage
	proxy           *proxy.SandboxProxy
	sandboxes       *smap.Map[*sandbox.Sandbox]
}

const (
	templatesDirectory = "/tmp/templates"

	sbxTimeout           = time.Hour
	provisionTimeout     = 1 * time.Minute
	configurationTimeout = 5 * time.Minute
	waitTimeForStartCmd  = 20 * time.Second
	waitEnvdTimeout      = 60 * time.Second

	cleanupTimeout = time.Second * 10
)

func NewBuilder(
	logger *zap.Logger,
	buildLogger *zap.Logger,
	tracer trace.Tracer,
	templateStorage *template.Storage,
	buildCache *cache.BuildCache,
	storage storage.StorageProvider,
	devicePool *nbd.DevicePool,
	networkPool *network.Pool,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
) *TemplateBuilder {
	return &TemplateBuilder{
		logger:          logger,
		tracer:          tracer,
		buildCache:      buildCache,
		buildLogger:     buildLogger,
		templateStorage: templateStorage,
		storage:         storage,
		devicePool:      devicePool,
		networkPool:     networkPool,
		proxy:           proxy,
		sandboxes:       sandboxes,
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
//   - start command (if defined)
//
// 6. Snapshot
// 7. Upload template
func (b *TemplateBuilder) Build(ctx context.Context, template *TemplateConfig) (*Result, error) {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	_, err := b.buildCache.Get(template.BuildId)
	if err != nil {
		return nil, err
	}

	logsWriter := template.BuildLogsWriter
	postProcessor := writer.NewPostProcessor(ctx, logsWriter)
	go postProcessor.Start()
	defer postProcessor.Stop(err)

	envdVersion, err := GetEnvdVersion(ctx)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while getting envd version: %v", err))
		return nil, err
	}

	templateCacheFiles, err := template.NewTemplateCacheFiles()
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while creating template files: %v", err))
		return nil, err
	}

	templateBuildDir := filepath.Join(templatesDirectory, template.BuildId)
	err = os.MkdirAll(templateBuildDir, 0777)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while creating template directory: %v", err))
		return nil, fmt.Errorf("error initializing directories for building template '%s' during build '%s': %w", template.TemplateId, template.BuildId, err)
	}
	defer func() {
		err := os.RemoveAll(templateBuildDir)
		if err != nil {
			b.logger.Error("Error while removing template build directory", zap.Error(err))
		}
	}()

	// Created here to be able to pass it to CreateSandbox for populating COW cache
	rootfsPath := filepath.Join(templateBuildDir, rootfsBuildFileName)

	rootfs, memfile, err := Build(
		ctx,
		b.tracer,
		template,
		postProcessor,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error building environment: %v", err))
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
		postProcessor.WriteMsg(fmt.Sprintf("Error while creating provision rootfs: %v", err))
		return nil, fmt.Errorf("error creating provision rootfs symlink: %w", err)
	}

	err = b.provisionSandbox(ctx, template, envdVersion, localTemplate, rootfsProvisionPath)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error provisioning sandbox: %v", err))
		return nil, fmt.Errorf("error provisioning sandbox: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
	zap.L().Debug("provisioned filesystem ext4 integrity",
		zap.String("result", ext4Check),
		zap.Error(err),
	)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error checking provisioned filesystem integrity: %v", err))
		return nil, fmt.Errorf("error checking ext4 filesystem integrity: %w", err)
	}

	err = b.enlargeDiskAfterProvisioning(ctx, template, rootfsPath)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error enlarging disk after provisioning: %v", err))
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
			SystemdToKernelLogs: env.IsDevelopment(),
		},
	)
	defer func() {
		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
		}
	}()
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error creating sandbox: %v", err))
		return nil, fmt.Errorf("error creating sandbox: %w", err)
	}
	err = sbx.WaitForEnvd(
		ctx,
		b.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Failed waiting for sandbox start: %v", err))
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
	err = b.runCommand(
		ctx,
		postProcessor,
		sbx.Metadata.Config.SandboxId,
		configurationTimeout,
		scriptDef.String(),
		"root",
	)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while running script: %v", err))
		return nil, fmt.Errorf("error running script: %w", err)
	}

	// Start command
	if template.StartCmd != "" {
		postProcessor.WriteMsg("Running start command")

		// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
		// TODO: Remove this after we can add customizable wait time for building templates.
		// TODO: Make this user configurable, with health check too
		startCmdWait := waitTimeForStartCmd
		if template.TemplateId == "zegbt9dl3l2ixqem82mm" || template.TemplateId == "ot5bidkk3j2so2j02uuz" || template.TemplateId == "0zeou1s7agaytqitvmzc" {
			startCmdWait = 120 * time.Second
		}
		err := b.runCommand(
			ctx,
			postProcessor,
			sbx.Metadata.Config.SandboxId,
			startCmdWait,
			template.StartCmd,
			"root",
		)
		if err != nil {
			postProcessor.WriteMsg(fmt.Sprintf("Error while running command: %v", err))
			return nil, fmt.Errorf("error running command: %w", err)
		}

		postProcessor.WriteMsg("Start command is running")
		telemetry.ReportEvent(ctx, "waited for start command", attribute.Float64("seconds", float64(waitTimeForStartCmd/time.Second)))
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
		postProcessor.WriteMsg(fmt.Sprintf("Error while uploading template: %v", uploadErr))
		return nil, fmt.Errorf("error uploading build files: %w", uploadErr)
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
					b.logger.Error("Error while removing build files", zap.Error(removeErr))
					telemetry.ReportError(ctx, removeErr)
				}
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

		// Forward upload errors to errCh
		err = <-uploadErrCh
		errCh <- err
	}()

	return errCh
}

func (b *TemplateBuilder) provisionSandbox(
	ctx context.Context,
	template *TemplateConfig,
	envdVersion string,
	localTemplate *templatelocal.LocalTemplate,
	rootfsPath string,
) (e error) {
	ctx, childSpan := b.tracer.Start(ctx, "provision-sandbox")
	defer childSpan.End()

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
		},
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
	if sizeDiff <= 0 {
		zap.L().Debug("no need to enlarge rootfs, skipping")
		return nil
	}
	zap.L().Debug("adding provision size diff to rootfs", zap.Int64("size", sizeDiff))
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
	zap.L().Debug("final enlarge filesystem ext4 integrity",
		zap.String("result", ext4Check),
		zap.Error(err),
	)
	if err != nil {
		return fmt.Errorf("error checking final enlarge filesystem integrity: %w", err)
	}
	return nil
}
