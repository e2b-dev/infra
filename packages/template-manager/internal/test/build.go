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
	"golang.org/x/sync/errgroup"

	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

func Build(templateID, buildID string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute*3)
	defer cancel()

	tracer := otel.Tracer("test")

	dockerClient, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		panic(err)
	}

	var buf bytes.Buffer
	t := build.Env{
		TemplateFiles: storage.TemplateFiles{
			BuildId:    buildID,
			TemplateId: templateID,
		},
		VCpuCount:             2,
		MemoryMB:              256,
		StartCmd:              "",
		KernelVersion:         "vmlinux-5.10.186",
		DiskSizeMB:            512,
		FirecrackerBinaryPath: "/fc-versions/v1.7.0-dev_8bb88311/firecracker",
		BuildLogsWriter:       &buf,
		HugePages:             true,
	}

	err = t.Build(ctx, tracer, dockerClient, legacyClient)
	if err != nil {
		errMsg := fmt.Errorf("error building template: %w", err)

		fmt.Fprintln(os.Stderr, errMsg)

		return
	}

	tempStorage := template.NewTemplateStorage(ctx)

	buildStorage := tempStorage.NewTemplateBuild(&t.TemplateFiles)

	uploadWg, ctx := errgroup.WithContext(ctx)

	uploadWg.Go(func() error {
		return buildStorage.UploadMemfile(ctx, t.BuildMemfilePath())
	})

	uploadWg.Go(func() error {
		return buildStorage.UploadRootfs(ctx, t.BuildRootfsPath())
	})

	uploadWg.Go(func() error {
		snapfile, err := os.Open(t.BuildSnapfilePath())
		if err != nil {
			return err
		}

		return buildStorage.UploadSnapfile(ctx, snapfile)
	})

	err = uploadWg.Wait()
	if err != nil {
		log.Fatal().Err(err).Msg("error uploading build files")
	}
}
