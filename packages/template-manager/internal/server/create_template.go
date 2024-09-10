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

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	template_manager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
)

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

	logsWriter := writer.New(stream)
	template := &build.Env{
		EnvID:                 config.TemplateID,
		BuildID:               config.BuildID,
		VCpuCount:             int64(config.VCpuCount),
		MemoryMB:              int64(config.MemoryMB),
		StartCmd:              config.StartCommand,
		DiskSizeMB:            int64(config.DiskSizeMB),
		HugePages:             config.HugePages,
		KernelVersion:         config.KernelVersion,
		FirecrackerBinaryPath: filepath.Join(consts.FirecrackerVersionsDir, config.FirecrackerVersion, consts.FirecrackerBinaryName),
		BuildLogsWriter:       logsWriter,
	}

	buildStorage := s.templateStorage.NewTemplateBuild(config.TemplateID, config.BuildID)

	var err error

	// Remove local template files if build fails
	defer func() {
		removeCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
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
			removeCtx, cancel := context.WithTimeout(context.Background(), time.Second*10)
			defer cancel()

			removeErr := buildStorage.Remove(removeCtx)
			if removeErr != nil {
				telemetry.ReportError(childCtx, removeErr)
			}
		}
	}()

	var uploadWg errgroup.Group

	uploadWg.Go(func() error {
		memfile, err := os.Open(template.MemfilePath())
		if err != nil {
			return err
		}

		return buildStorage.UploadMemfile(ctx, memfile)
	})

	uploadWg.Go(func() error {
		rootfs, err := os.Open(template.RootfsPath())
		if err != nil {
			return err
		}

		return buildStorage.UploadRootfs(ctx, rootfs)
	})

	uploadWg.Go(func() error {
		snapfile, err := os.Open(template.SnapfilePath())
		if err != nil {
			return err
		}

		return buildStorage.UploadSnapfile(ctx, snapfile)
	})

	cmd := exec.Command(consts.HostEnvdPath, "-version")

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
		consts.RootfsSizeKey, strconv.FormatInt(template.RootfsSizeMB(), 10),
		consts.EnvdVersionKey, version,
	)

	stream.SetTrailer(trailerMetadata)

	telemetry.ReportEvent(childCtx, "Environment built")

	return nil
}
