package build

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateBuilder struct {
	tracer trace.Tracer

	storage            storage.StorageProvider
	devicePool         *nbd.DevicePool
	networkPool        *network.Pool
	logger             *zap.Logger
	buildCache         *cache.BuildCache
	buildLogger        *zap.Logger
	dockerClient       *client.Client
	legacyDockerClient *docker.Client
	templateStorage    *template.Storage
}

const cleanupTimeout = time.Second * 10

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
) *TemplateBuilder {
	return &TemplateBuilder{
		logger:             logger,
		tracer:             tracer,
		buildCache:         buildCache,
		buildLogger:        buildLogger,
		dockerClient:       dockerClient,
		legacyDockerClient: legacyDockerClient,
		templateStorage:    templateStorage,
		storage:            storage,
		devicePool:         devicePool,
		networkPool:        networkPool,
	}
}

func (b *TemplateBuilder) Build(ctx context.Context, template *Env, envID string, buildID string) error {
	buildIDParsed, err := uuid.Parse(buildID)
	if err != nil {
		return fmt.Errorf("error parsing build ID: %w", err)
	}

	_, err = b.buildCache.Get(buildID)
	if err != nil {
		return err
	}

	logsWriter := template.BuildLogsWriter
	postProcessor := writer.NewPostProcessor(ctx, logsWriter)
	go postProcessor.Start()
	defer postProcessor.Stop(err)

	// Remove local template files when exiting
	defer func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		removeErr := template.Remove(removeCtx, b.tracer)
		if removeErr != nil {
			b.logger.Error("Error while removing template files", zap.Error(removeErr))
			telemetry.ReportError(ctx, removeErr)
		}
	}()

	_, err = template.Build(ctx, b.tracer, postProcessor, b.dockerClient, b.legacyDockerClient, b.networkPool, b.devicePool)
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error building environment: %v", err))
		telemetry.ReportCriticalError(ctx, err)

		buildStateErr := b.buildCache.SetFailed(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}

		return err
	}

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

	sbx := &sandbox.Sandbox{}

	// TODO: LINK to sandbox.go
	postProcessor.WriteMsg("Processing vm memory and file system")
	snapshotTemplateFiles, err := storage.NewTemplateFiles(
		in.TemplateId,
		bui,
		sbx.Config.KernelVersion,
		sbx.Config.FirecrackerVersion,
	).NewTemplateCacheFiles()
	if err != nil {
		return fmt.Errorf("error creating template files: %w", err)
	}

	snapshot, err := sbx.Pause(
		ctx,
		b.tracer,
		snapshotTemplateFiles,
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}

	/*memfileSource, err := os.Open(template.BuildMemfilePath())
	if err != nil {
		return fmt.Errorf("error opening memfile source: %w", err)
	}
	defer memfileSource.Close()
	memfileInfo, err := memfileSource.Stat()
	if err != nil {
		return fmt.Errorf("error getting memfile size: %w", err)
	}

	memfileDiffFile, err := os.Create(template.BuildMemfileDiffPath())
	if err != nil {
		return fmt.Errorf("error creating memfile diff file: %w", err)
	}
	defer memfileDiffFile.Close()

	snapshot, err := sandbox.TODOTEMPLATEPAUSE(
		ctx,
		b.tracer,
		buildIDParsed,
		memfileInfo.Size(),
		template.MemfilePageSize(),
	)
	if err != nil {
		return fmt.Errorf("error processing vm: %w", err)
	}*/

	// UPLOAD
	postProcessor.WriteMsg("Uploading template")
	templateBuild := storage.NewTemplateBuild(
		snapshot.MemfileDiffHeader,
		snapshot.RootfsDiffHeader,
		b.storage,
		template.TemplateFiles,
	)

	upload := templateBuild.Upload(
		ctx,
		template.BuildSnapfilePath(),
		&snapshot.MemfileDiff,
		&snapshot.RootfsDiff,
	)

	cmd := exec.Command(storage.HostEnvdPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		postProcessor.WriteMsg(fmt.Sprintf("Error while getting envd version: %v", err))
		telemetry.ReportError(ctx, err)

		buildStateErr := b.buildCache.SetFailed(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}

		return err
	}

	postProcessor.Stop(err)
	// Wait for the CLI to load all the logs
	// This is a temporary ~fix for the CLI to load most of the logs before finishing the template build
	// Ideally we should wait in the CLI for the last log message
	time.Sleep(5 * time.Second)

	uploadErr := <-upload
	if uploadErr != nil {
		errMsg := fmt.Sprintf("Error while uploading build files: %v", uploadErr)
		postProcessor.WriteMsg(errMsg)
		telemetry.ReportError(ctx, uploadErr)

		buildStateErr := b.buildCache.SetFailed(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}

		return uploadErr
	}

	buildMetadata := &template_manager.TemplateBuildMetadata{RootfsSizeKey: int32(template.RootfsSizeMB()), EnvdVersionKey: strings.TrimSpace(string(out))}
	err = b.buildCache.SetSucceeded(envID, buildID, buildMetadata)
	if err != nil {
		b.logger.Error("Error while setting build state to succeeded", zap.Error(err))
		telemetry.ReportError(ctx, err)
		return err
	}

	return nil
}
