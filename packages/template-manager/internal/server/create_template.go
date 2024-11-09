package server

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc/metadata"

	templateManager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	templateStorage "github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
)

const cleanupTimeout = time.Second * 10

func (s *serverStore) TemplateCreate(templateRequest *templateManager.TemplateCreateRequest, stream templateManager.TemplateService_TemplateCreateServer) error {
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

	logsWriter := writer.New(stream)
	template := &build.Env{
		TemplateFiles: templateStorage.TemplateFiles{
			TemplateId: config.TemplateID,
			BuildId:    config.BuildID,
		},
		VCpuCount:             int64(config.VCpuCount),
		MemoryMB:              int64(config.MemoryMB),
		StartCmd:              config.StartCommand,
		DiskSizeMB:            int64(config.DiskSizeMB),
		HugePages:             config.HugePages,
		KernelVersion:         config.KernelVersion,
		FirecrackerBinaryPath: filepath.Join(templateStorage.FirecrackerVersionsDir, config.FirecrackerVersion, templateStorage.FirecrackerBinaryName),
		BuildLogsWriter:       logsWriter,
	}

	buildStorage := s.templateStorage.NewTemplateBuild(&template.TemplateFiles)

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

	uploadWg, ctx := errgroup.WithContext(childCtx)

	uploadWg.Go(func() error {
		return buildStorage.UploadMemfile(ctx, template.BuildMemfilePath())
	})

	uploadWg.Go(func() error {
		return buildStorage.UploadRootfs(ctx, template.BuildRootfsPath())
	})

	uploadWg.Go(func() error {
		snapfile, err := os.Open(template.BuildSnapfilePath())
		if err != nil {
			return err
		}

		defer snapfile.Close()

		return buildStorage.UploadSnapfile(ctx, snapfile)
	})

	cmd := exec.Command(templateStorage.HostEnvdPath, "-version")

	out, err := cmd.Output()
	if err != nil {
		_, _ = logsWriter.Write([]byte(fmt.Sprintf("Error while getting envd version: %v", err)))

		return err
	}

	uploadERr := uploadWg.Wait()
	if uploadERr != nil {
		errMsg := fmt.Sprintf("Error while uploading build files: %v", uploadERr)
		_, _ = logsWriter.Write([]byte(errMsg))

		return uploadERr
	}

	version := strings.TrimSpace(string(out))
	trailerMetadata := metadata.Pairs(
		templateStorage.RootfsSizeKey, strconv.FormatInt(template.RootfsSizeMB(), 10),
		templateStorage.EnvdVersionKey, version,
	)

	stream.SetTrailer(trailerMetadata)

	telemetry.ReportEvent(childCtx, "Environment built")

	return nil
}
