package env

import (
	"context"
	"net/http"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"go.opentelemetry.io/otel"
)

func MockBuild(envID, buildID string) {
	ctx := context.Background()

	tracer := otel.Tracer("test")

	client, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		panic(err)
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		panic(err)
	}

	httpClient := &http.Client{}

	contextsPath := "/mnt/disks/docker-contexts/v1"
	registry := "us-central1-docker.pkg.dev/e2b-prod/custom-environments"
	envsDisk := "/mnt/disks/fc-envs/v1"
	kernelImagePath := "/fc-vm/vmlinux.bin"
	firecrackerBinaryPath := "/usr/bin/firecracker"
	envdPath := "/fc-vm/envd"
	pkgsPath := "/fc-vm/pkgs"
	contextFileName := "context.tar.gz"
	vCPUCount := int64(1)
	memoryMB := int64(512)
	diskSizeMB := int64(512)
	apiSecret := "SUPER_SECR3T_4PI_K3Y"

	e := Env{
		BuildID:               buildID,
		EnvID:                 envID,
		EnvsDiskPath:          envsDisk,
		VCpuCount:             vCPUCount,
		PkgsPath:              pkgsPath,
		MemoryMB:              memoryMB,
		DockerContextsPath:    contextsPath,
		DockerRegistry:        registry,
		KernelImagePath:       kernelImagePath,
		DiskSizeMB:            diskSizeMB,
		FirecrackerBinaryPath: firecrackerBinaryPath,
		EnvdPath:              envdPath,
		ContextFileName:       contextFileName,
		APISecret: 			   apiSecret,
	}

	err = e.Build(ctx, tracer, client, legacyClient, httpClient)
	if err != nil {
		panic(err)
	}
}
