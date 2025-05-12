package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateBuilder struct {
	logger   *zap.Logger
	tracer   trace.Tracer
	clientID string

	storage            storage.StorageProvider
	devicePool         *nbd.DevicePool
	networkPool        *network.Pool
	buildCache         *cache.BuildCache
	buildLogger        *zap.Logger
	dockerClient       *client.Client
	legacyDockerClient *docker.Client
	templateStorage    *template.Storage
	proxy              *proxy.SandboxProxy
	sandboxes          *smap.Map[*sandbox.Sandbox]
}

const (
	templatesDirectory = "/tmp/templates"

	sbxTimeout          = time.Hour
	waitTimeForStartCmd = 20 * time.Second

	cleanupTimeout = time.Second * 10
)

func NewBuilder(
	logger *zap.Logger,
	buildLogger *zap.Logger,
	tracer trace.Tracer,
	dockerClient *client.Client,
	legacyDockerClient *docker.Client,
	templateStorage *template.Storage,
	buildCache *cache.BuildCache,
	storage storage.StorageProvider,
	devicePool *nbd.DevicePool,
	networkPool *network.Pool,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	clientID string,
) *TemplateBuilder {
	return &TemplateBuilder{
		logger:             logger,
		tracer:             tracer,
		clientID:           clientID,
		buildCache:         buildCache,
		buildLogger:        buildLogger,
		dockerClient:       dockerClient,
		legacyDockerClient: legacyDockerClient,
		templateStorage:    templateStorage,
		storage:            storage,
		devicePool:         devicePool,
		networkPool:        networkPool,
		proxy:              proxy,
		sandboxes:          sandboxes,
	}
}

func (b *TemplateBuilder) Build(ctx context.Context, template *TemplateConfig, envID string, buildID string) error {
	ctx, childSpan := b.tracer.Start(ctx, "build")
	defer childSpan.End()

	_, err := b.buildCache.Get(buildID)
	if err != nil {
		return err
	}

	logsWriter := template.BuildLogsWriter
	postProcessor := writer.NewPostProcessor(ctx, logsWriter)
	go postProcessor.Start()
	defer postProcessor.Stop(err)

	envdVersion, err := GetEnvdVersion(ctx)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while getting envd version: %v", err))
		return err
	}

	templateCacheFiles, err := template.NewTemplateCacheFiles()
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while creating template files: %v", err))
		return err
	}

	templateBuildDir := filepath.Join(templatesDirectory, template.BuildId)
	err = os.MkdirAll(templateBuildDir, 0777)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while creating template directory: %v", err))
		return fmt.Errorf("error initializing directories for building template '%s' during build '%s': %w", template.TemplateId, template.BuildId, err)
	}
	defer func() {
		err := os.RemoveAll(templateBuildDir)
		if err != nil {
			b.logger.Error("Error while removing template build directory", zap.Error(err))
		}
	}()

	// Created here to be able to pass it to CreateSandbox for populating COW cache
	rootfsPath := filepath.Join(templateBuildDir, rootfsBuildFileName)

	localTemplate, err := Build(
		ctx,
		b.tracer,
		template,
		postProcessor,
		b.dockerClient,
		b.legacyDockerClient,
		templateCacheFiles,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error building environment: %v", err))
		return err
	}

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
	)
	if err != nil {
		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
		}

		postProcessor.WriteMsg(fmt.Sprintf("Error creating sandbox: %v", err))
		return fmt.Errorf("error creating sandbox: %w", err)
	}
	b.sandboxes.Insert(sbx.Metadata.Config.SandboxId, sbx)
	defer func() {
		cleanupErr := cleanup.Run(ctx)
		if cleanupErr != nil {
			b.logger.Error("Error cleaning up sandbox", zap.Error(cleanupErr))
		}

		b.sandboxes.Remove(sbx.Metadata.Config.SandboxId)
		b.proxy.RemoveFromPool(sbx.StartID)
	}()

	// Remove build files if build fails or times out
	defer func() {
		if err != nil {
			removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()

			removeErr := b.templateStorage.Remove(removeCtx, buildID)
			if removeErr != nil {
				b.logger.Error("Error while removing build files", zap.Error(removeErr))
				telemetry.ReportError(ctx, removeErr)
			}
		}
	}()

	// Start command
	if template.StartCmd != "" {
		postProcessor.WriteMsg("Running start command")

		// HACK: This is a temporary fix for a customer that needs a bigger time to start the command.
		// TODO: Remove this after we can add customizable wait time for building templates.
		startCmdWait := waitTimeForStartCmd
		if template.TemplateId == "zegbt9dl3l2ixqem82mm" || template.TemplateId == "ot5bidkk3j2so2j02uuz" || template.TemplateId == "0zeou1s7agaytqitvmzc" {
			startCmdWait = 120 * time.Second
		}
		err := b.runCommand(
			ctx,
			postProcessor,
			sbx.Metadata.Config.SandboxId,
			startCmdWait,
			env.StartCmd,
		)
		if err != nil {
			postProcessor.WriteMsg(fmt.Sprintf("Error while running command: %v", err))
			return fmt.Errorf("error running command: %w", err)
		}

		postProcessor.WriteMsg("Start command is running")
		telemetry.ReportEvent(ctx, "waited for start command", attribute.Float64("seconds", float64(waitTimeForStartCmd/time.Second)))
	}

	// PAUSE
	postProcessor.WriteMsg("Pausing sandbox template")
	snapshot, err := sbx.Pause(
		ctx,
		b.tracer,
		templateCacheFiles,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}

	postProcessor.WriteMsg("Uploading template")
	uploadErrCh := b.uploadTemplate(
		ctx,
		template.TemplateFiles,
		snapshot,
	)

	postProcessor.Stop(err)

	uploadErr := <-uploadErrCh
	if uploadErr != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while uploading build files: %v", uploadErr))
		return uploadErr
	}

	// Wait for the CLI to load all the logs
	// This is a temporary ~fix for the CLI to load most of the logs before finishing the template build
	// Ideally we should wait in the CLI for the last log message
	time.Sleep(5 * time.Second)

	buildMetadata := &templatemanager.TemplateBuildMetadata{RootfsSizeKey: int32(template.RootfsSizeMB()), EnvdVersionKey: envdVersion}
	err = b.buildCache.SetSucceeded(envID, buildID, buildMetadata)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while setting build state to succeeded: %v", err))
		return fmt.Errorf("error while setting build state to succeeded: %w", err)
	}

	return nil
}

func (b *TemplateBuilder) uploadTemplate(
	ctx context.Context,
	templateFiles *storage.TemplateFiles,
	snapshot *sandbox.Snapshot,
) chan error {
	errCh := make(chan error, 1)

	go func() {
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
		errCh <- <-uploadErrCh
	}()

	return errCh
}
