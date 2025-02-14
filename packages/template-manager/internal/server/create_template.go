package server

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"google.golang.org/grpc/metadata"

	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
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
		attribute.Int64("env.disk_size_mb", int64(config.DiskSizeMB)), 
		attribute.Bool("env.huge_pages", config.HugePages),
	)

	logsWriter := writer.New(stream)
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

	// Remove local template files if build fails
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

	memfilePath := template.BuildMemfilePath()
	rootfsPath := template.BuildRootfsPath()

	upload := buildStorage.Upload(
		childCtx,
		template.BuildSnapfilePath(),
		&memfilePath,
		&rootfsPath,
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
