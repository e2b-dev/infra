package test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/shared/pkg/schema"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build/writer"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

func Build(templateID, buildID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*3)
	defer cancel()

	tracer := otel.Tracer("test")

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		panic(err)
	}

	var buf bytes.Buffer
	t := build.Env{
		TemplateFiles: storage.NewTemplateFiles(
			templateID,
			buildID,
			schema.DefaultKernelVersion,
			schema.DefaultFirecrackerVersion,
			true,
		),
		VCpuCount:       2,
		MemoryMB:        256,
		StartCmd:        "",
		DiskSizeMB:      512,
		BuildLogsWriter: &buf,
	}

	postProcessor := writer.NewPostProcessor(ctx, &buf)
	defer postProcessor.Stop(nil)

	err = t.Build(ctx, tracer, postProcessor, dockerClient, legacyClient)
	if err != nil {
		errMsg := fmt.Errorf("error building template: %w", err)

		fmt.Fprintln(os.Stderr, errMsg)

		return
	}

	tempStorage := template.NewStorage(ctx)

	buildStorage := tempStorage.NewBuild(t.TemplateFiles)

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
		log.Fatal().Err(err).Msg("error uploading build files")
	}
}
