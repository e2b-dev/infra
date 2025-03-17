package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/bits-and-blooms/bitset"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/metadata"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage/header"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
)

const cleanupTimeout = time.Second * 10

func (s *serverStore) TemplateCreate(templateRequest *template_manager.TemplateCreateRequest, stream template_manager.TemplateService_TemplateCreateServer) error {
	ctx := stream.Context()

	childCtx, childSpan := s.tracer.Start(ctx, "template-create")
	defer childSpan.End()

	config := templateRequest.Template

	childSpan.SetAttributes(
		attribute.String("env.id", config.TemplateID),
		attribute.String("env.build.id", config.BuildID),
		attribute.String("env.kernel.version", config.KernelVersion),
		attribute.String("env.firecracker.version", config.FirecrackerVersion),
		attribute.String("env.start_cmd", config.StartCommand),
		attribute.Int64("env.memory_mb", int64(config.MemoryMB)),
		attribute.Int64("env.vcpu_count", int64(config.VCpuCount)),
		attribute.Bool("env.huge_pages", config.HugePages),
	)

	logsWriter := writer.New(
		stream,
		s.buildLogger.
			With(zap.Field{Type: zapcore.StringType, Key: "envID", String: config.TemplateID}).
			With(zap.Field{Type: zapcore.StringType, Key: "buildID", String: config.BuildID}),
	)

	template := &build.Env{
		TemplateFiles: storage.NewTemplateFiles(
			config.TemplateID,
			config.BuildID,
			config.KernelVersion,
			config.FirecrackerVersion,
			config.HugePages,
		),
		VCpuCount:       int64(config.VCpuCount),
		MemoryMB:        int64(config.MemoryMB),
		StartCmd:        config.StartCommand,
		DiskSizeMB:      int64(config.DiskSizeMB),
		BuildLogsWriter: logsWriter,
	}

	buildStorage := s.templateStorage.NewBuild(template.TemplateFiles)

	var err error

	// Remove local template files after build ends.
	defer func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
		defer cancel()

		removeErr := template.Remove(removeCtx, s.tracer)
		if removeErr != nil {
			telemetry.ReportError(childCtx, removeErr)
		}
	}()

	err = template.Build(childCtx, s.tracer, s.dockerClient, s.legacyDockerClient)
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error building environment: %v", err)))

		telemetry.ReportCriticalError(childCtx, err)

		return err
	}

	// Remove build files if build fails or times out
	defer func() {
		if err != nil {
			removeCtx, cancel := context.WithTimeout(context.Background(), cleanupTimeout)
			defer cancel()

			removeErr := buildStorage.Remove(removeCtx)
			if removeErr != nil {
				telemetry.ReportError(childCtx, removeErr)
			}
		}
	}()

	buildID, err := uuid.Parse(config.BuildID)
	if err != nil {
		return fmt.Errorf("error parsing build id: %w", err)
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
		&buildID,
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
		BuildId:     buildID,
		BaseBuildId: buildID,
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
		&buildID,
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
		BuildId:     buildID,
		BaseBuildId: buildID,
	}

	rootfsHeader := header.NewHeader(
		rootfsMetadata,
		rootfsMappings,
	)

	// UPLOAD
	b := storage.NewTemplateBuild(
		memfileHeader,
		rootfsHeader,
		template.TemplateFiles,
	)

	upload := b.Upload(
		childCtx,
		template.BuildSnapfilePath(),
		&memfileDiffPath,
		&rootfsDiffPath,
	)

	cmd := exec.Command(storage.HostEnvdPath, "-version")

	out, err := cmd.Output()
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error while getting envd version: %v", err)))

		return err
	}

	uploadErr := <-upload
	if uploadErr != nil {
		errMsg := fmt.Sprintf("Error while uploading build files: %v", uploadErr)
		_, _ = logsWriter.Write([]byte(errMsg))

		return uploadErr
	}

	version := strings.TrimSpace(string(out))
	trailerMetadata := metadata.Pairs(
		storage.RootfsSizeKey, strconv.FormatInt(template.RootfsSizeMB(), 10),
		storage.EnvdVersionKey, version,
	)

	stream.SetTrailer(trailerMetadata)

	telemetry.ReportEvent(childCtx, "Environment built")

	return nil
}
