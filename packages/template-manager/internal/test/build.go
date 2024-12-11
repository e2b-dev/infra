package test

import (
	"bytes"
	"context"
	"time"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"go.opentelemetry.io/otel"

	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
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
	e := build.Env{
		BuildID:               buildID,
		EnvID:                 templateID,
		VCpuCount:             8,
		MemoryMB:              4096,
		StartCmd:              "",
		KernelVersion:         "vmlinux-6.1.102",
		DiskSizeMB:            5120,
		FirecrackerBinaryPath: "/fc-versions/v1.9.1_3370eaf8/firecracker",
		BuildLogsWriter:       &buf,
		HugePages:             true,
	}

	err = e.Build(ctx, tracer, dockerClient, legacyClient)
	if err != nil {
		panic(err)
	}
}
