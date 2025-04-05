package build

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/cache"

	"go.uber.org/zap"
)

type TemplateBuilder struct {
	tracer trace.Tracer

	logger             *zap.Logger
	buildCache         *cache.BuildCache
	buildLogger        *zap.Logger
	dockerClient       *client.Client
	legacyDockerClient *docker.Client
}

const cleanupTimeout = time.Second * 10

func NewBuilder(logger *zap.Logger, buildLogger *zap.Logger, tracer trace.Tracer, dockerClient *client.Client, legacyDockerClient *docker.Client, buildCache *cache.BuildCache) *TemplateBuilder {
	return &TemplateBuilder{
		logger:             logger,
		tracer:             tracer,
		buildCache:         buildCache,
		buildLogger:        buildLogger,
		dockerClient:       dockerClient,
		legacyDockerClient: legacyDockerClient,
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

	err = template.Build(ctx, b.tracer, b.dockerClient, b.legacyDockerClient)
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error building environment: %v", err)))
		telemetry.ReportCriticalError(ctx, err)

		buildStateErr := b.buildCache.SetFailed(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}

		return err
	}

	// MEMFILE
	memfilePath := template.BuildMemfilePath()
	memfileDiffPath := template.BuildMemfileDiffPath()

	memfileSource, err := os.Open(memfilePath)
	if err != nil {
		return fmt.Errorf("error opening memfile source: %w", err)
	}

	memfileInfo, err := memfileSource.Stat()
	if err != nil {
		return fmt.Errorf("error getting memfile size: %w", err)
	}

	memfileDiffFile, err := os.Create(memfileDiffPath)
	if err != nil {
		return fmt.Errorf("error creating memfile diff file: %w", err)
	}

	memfileDirtyPages := bitset.New(0)
	memfileDirtyPages.FlipRange(0, uint(header.TotalBlocks(memfileInfo.Size(), template.MemfilePageSize())))

	memfileDirtyPages, emptyDirtyPages, err := header.CreateDiff(
		memfileSource,
		template.MemfilePageSize(),
		memfileDirtyPages,
		memfileDiffFile,
	)

	memfileDirtyMappings := header.CreateMapping(
		&buildIDParsed,
		memfileDirtyPages,
		uint64(template.MemfilePageSize()),
	)

	memfileEmptyMappings := header.CreateMapping(
		&uuid.Nil,
		emptyDirtyPages,
		uint64(template.MemfilePageSize()),
	)

	memfileMappings := header.MergeMappings(memfileDirtyMappings, memfileEmptyMappings)

	memfileMetadata := &header.Metadata{
		Version:     1,
		Generation:  0,
		BlockSize:   uint64(template.MemfilePageSize()),
		Size:        uint64(memfileInfo.Size()),
		BuildId:     buildIDParsed,
		BaseBuildId: buildIDParsed,
	}

	memfileHeader := header.NewHeader(
		memfileMetadata,
		memfileMappings,
	)

	// ROOTFS
	rootfsPath := template.BuildRootfsPath()
	rootfsDiffPath := template.BuildRootfsDiffPath()

	rootfsSource, err := os.Open(rootfsPath)
	if err != nil {
		return fmt.Errorf("error opening rootfs source: %w", err)
	}

	rootfsInfo, err := rootfsSource.Stat()
	if err != nil {
		return fmt.Errorf("error getting rootfs size: %w", err)
	}

	rootfsDiffFile, err := os.Create(rootfsDiffPath)
	if err != nil {
		return fmt.Errorf("error creating rootfs diff file: %w", err)
	}

	rootfsDirtyBlocks := bitset.New(0)
	rootfsDirtyBlocks.FlipRange(0, uint(header.TotalBlocks(rootfsInfo.Size(), template.RootfsBlockSize())))

	rootfsDirtyBlocks, emptyDirtyBlocks, err := header.CreateDiff(
		rootfsSource,
		template.RootfsBlockSize(),
		rootfsDirtyBlocks,
		rootfsDiffFile,
	)

	rootfsDirtyMappings := header.CreateMapping(
		&buildIDParsed,
		rootfsDirtyBlocks,
		uint64(template.RootfsBlockSize()),
	)

	rootfsEmptyMappings := header.CreateMapping(
		&uuid.Nil,
		emptyDirtyBlocks,
		uint64(template.RootfsBlockSize()),
	)

	rootfsMappings := header.MergeMappings(rootfsDirtyMappings, rootfsEmptyMappings)

	rootfsMetadata := &header.Metadata{
		Version:     1,
		Generation:  0,
		BlockSize:   uint64(template.RootfsBlockSize()),
		Size:        uint64(rootfsInfo.Size()),
		BuildId:     buildIDParsed,
		BaseBuildId: buildIDParsed,
	}

	rootfsHeader := header.NewHeader(
		rootfsMetadata,
		rootfsMappings,
	)

	// UPLOAD
	templateBuild := storage.NewTemplateBuild(
		memfileHeader,
		rootfsHeader,
		template.TemplateFiles,
	)

	// Remove build files if build fails or times out
	defer func() {
		if err != nil {
			removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()

			removeErr := templateBuild.Remove(removeCtx)
			if removeErr != nil {
				b.logger.Error("Error while removing build files", zap.Error(removeErr))
				telemetry.ReportError(ctx, removeErr)
			}
		}
	}()

	upload := templateBuild.Upload(
		ctx,
		template.BuildSnapfilePath(),
		&memfileDiffPath,
		&rootfsDiffPath,
	)

	cmd := exec.Command(storage.HostEnvdPath, "-version")
	out, err := cmd.Output()
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error while getting envd version: %v", err)))
		telemetry.ReportError(ctx, err)

		buildStateErr := b.buildCache.SetFailed(envID, buildID)
		if buildStateErr != nil {
			b.logger.Error("Error while setting build state to failed", zap.Error(buildStateErr))
			telemetry.ReportError(ctx, buildStateErr)
		}

		return err
	}

	uploadErr := <-upload
	if uploadErr != nil {
		errMsg := fmt.Sprintf("Error while uploading build files: %v", uploadErr)
		_, _ = logsWriter.Write([]byte(errMsg))
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
