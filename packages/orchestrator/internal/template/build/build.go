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
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type TemplateBuilder struct {
	tracer trace.Tracer

	storage            storage.StorageProvider
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

	err = template.Build(ctx, b.tracer, postProcessor, b.dockerClient, b.legacyDockerClient)
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

	postProcessor.WriteMsg("Processing system memory")

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

	memfileDirtyPages, emptyDirtyPages, err := header.CreateDiffWithTrace(
		ctx,
		b.tracer,
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

	postProcessor.WriteMsg("Processing file system")

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

	rootfsDirtyBlocks, emptyDirtyBlocks, err := header.CreateDiffWithTrace(
		ctx,
		b.tracer,
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

	postProcessor.WriteMsg("Uploading template")

	// UPLOAD
	templateBuild := storage.NewTemplateBuild(
		memfileHeader,
		rootfsHeader,
		b.storage,
		template.TemplateFiles,
	)

	upload := templateBuild.Upload(
		ctx,
		template.BuildSnapfilePath(),
		&memfileDiffPath,
		&rootfsDiffPath,
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
