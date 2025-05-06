package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/template"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	templateID := flag.String("template", "", "template id")
	buildID := flag.String("build", "", "build id")
	kernelVersion := flag.String("kernel", "", "kernel version")
	fcVersion := flag.String("firecracker", "", "firecracker version")
	flag.Parse()

	err := Build(ctx, *kernelVersion, *fcVersion, *templateID, *buildID)
	if err != nil {
		log.Fatal().Err(err).Msg("error building template")
		os.Exit(1)
	}
}

func Build(ctx context.Context, kernelVersion, fcVersion, templateID, buildID string) error {
	ctx, cancel := context.WithTimeout(ctx, time.Minute*3)
	defer cancel()

	tracer := otel.Tracer("test")

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return err
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		return err
	}

	var buf bytes.Buffer
	t := build.Env{
		TemplateFiles: storage.NewTemplateFiles(
			templateID,
			buildID,
			kernelVersion,
			fcVersion,
		),
		VCpuCount:       2,
		MemoryMB:        1024,
		StartCmd:        "",
		DiskSizeMB:      1024,
		BuildLogsWriter: &buf,
		HugePages:       true,
	}

	postProcessor := writer.NewPostProcessor(ctx, &buf)
	defer postProcessor.Stop(nil)

	err = t.Build(ctx, tracer, postProcessor, dockerClient, legacyClient)
	if err != nil {
		return fmt.Errorf("error building template: %w", err)
	}

	persistence, err := storage.GetTemplateStorageProvider(ctx)
	if err != nil {
		return err
	}

	tmplStorage := template.NewStorage(persistence)
	buildStorage := tmplStorage.NewBuild(t.TemplateFiles, persistence)

	memfilePath := t.BuildMemfilePath()
	rootfsPath := t.BuildRootfsPath()

	upload := buildStorage.Upload(
		ctx,
		t.BuildSnapfilePath(),
		&memfilePath,
		&rootfsPath,
	)

	err = <-upload
	if err != nil {
		return fmt.Errorf("error uploading build: %w", err)
	}

	return nil
}
