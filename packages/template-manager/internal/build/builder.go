package build

import (
	"context"
	"fmt"
	"github.com/docker/docker/client"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/cache"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
	docker "github.com/fsouza/go-dockerclient"
	"go.opentelemetry.io/otel/trace"

	"go.uber.org/zap"
	"os/exec"
	"strings"
	"time"
)

type TemplateBuilder struct {
	tracer trace.Tracer

	logger             *zap.Logger
	buildCache         *cache.BuildCache
	buildLogger        *zap.Logger
	dockerClient       *client.Client
	legacyDockerClient *docker.Client
	templateStorage    *template.Storage
}

const cleanupTimeout = time.Second * 10

func NewBuilder(logger *zap.Logger, buildLogger *zap.Logger, tracer trace.Tracer, dockerClient *client.Client, legacyDockerClient *docker.Client, templateStorage *template.Storage, buildCache *cache.BuildCache) *TemplateBuilder {
	return &TemplateBuilder{
		logger:             logger,
		tracer:             tracer,
		buildCache:         buildCache,
		buildLogger:        buildLogger,
		dockerClient:       dockerClient,
		legacyDockerClient: legacyDockerClient,
		templateStorage:    templateStorage,
	}
}

func (b *TemplateBuilder) Builder(ctx context.Context, template *Env, envID string, buildID string) error {
	buildStorage := b.templateStorage.NewBuild(template.TemplateFiles)
	buildEntry, err := b.buildCache.Get(buildID)
	if err != nil {
		return err
	}

	logsWriter := template.BuildLogsWriter

	// Remove local template files if build fails
	defer func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		removeErr := template.Remove(removeCtx, b.tracer)
		if removeErr != nil {
			b.logger.Error("Error while removing template files", zap.Error(removeErr))
			telemetry.ReportError(ctx, removeErr)
		}
	}()

	err = template.Build(ctx, b.tracer, b.dockerClient, b.legacyDockerClient)
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error building environment: %v", err)))
		telemetry.ReportCriticalError(ctx, err)

		buildStateErr := b.buildCache.SetSucceeded(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}
	}

	// Remove build files if build fails or times out
	defer func() {
		if err != nil {
			removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()

			removeErr := buildStorage.Remove(removeCtx)
			if removeErr != nil {
				b.logger.Error("Error while removing build files", zap.Error(removeErr))
				telemetry.ReportError(ctx, removeErr)
			}

			buildStateErr := b.buildCache.SetSucceeded(envID, buildID)
			if buildStateErr != nil {
				b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
				telemetry.ReportError(ctx, buildStateErr)
			}
		}
	}()

	memfilePath := template.BuildMemfilePath()
	rootfsPath := template.BuildRootfsPath()

	upload := buildStorage.Upload(
		ctx,
		template.BuildSnapfilePath(),
		&memfilePath,
		&rootfsPath,
	)

	cmd := exec.Command(storage.HostEnvdPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error while getting envd version: %v", err)))
		telemetry.ReportError(ctx, err)

		buildStateErr := b.buildCache.SetSucceeded(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}
	}

	uploadErr := <-upload
	if uploadErr != nil {
		errMsg := fmt.Sprintf("Error while uploading build files: %v", uploadErr)
		_, _ = logsWriter.Write([]byte(errMsg))
		telemetry.ReportError(ctx, uploadErr)

		buildStateErr := b.buildCache.SetSucceeded(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}
	}

	buildEntry.SetEnvdVersionKey(strings.TrimSpace(string(out)))
	buildEntry.SetRootFsSizeKey(int32(template.RootfsSizeMB()))

	err = b.buildCache.SetSucceeded(envID, buildID)
	if err != nil {
		b.logger.Error("Error while setting build state to succeeded", zap.Error(err))
		telemetry.ReportError(ctx, err)
		return err
	}

	return nil
}
