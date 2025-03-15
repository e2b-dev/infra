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

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
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
			true,
		),
		VCpuCount:       2,
		MemoryMB:        256,
		StartCmd:        "",
		DiskSizeMB:      512,
		BuildLogsWriter: &buf,
	}

	err = t.Build(ctx, tracer, dockerClient, legacyClient)
	if err != nil {
		return fmt.Errorf("error building template: %w", err)
	}

	buildStorage := storage.NewTemplateBuild(nil, nil, t.TemplateFiles)

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
