package build

import (
	"context"
	_ "embed"
	"fmt"
	"io"
	"os"
	"text/template"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type Env struct {
	*storage.TemplateFiles

	// Command to run when building the env.
	StartCmd string

	// The number of vCPUs to allocate to the VM.
	VCpuCount int64

	// The amount of RAM memory to allocate to the VM, in MiB.
	MemoryMB int64

	// The amount of free disk to allocate to the VM, in MiB.
	DiskSizeMB int64

	// Path to the directory where the temporary files for the build are stored.
	BuildLogsWriter io.Writer

	// Real size of the rootfs after building the env.
	rootfsSize int64
}

//go:embed provision.sh
var provisionEnvScriptFile string
var EnvInstanceTemplate = template.Must(template.New("provisioning-script").Parse(provisionEnvScriptFile))

// Real size in MB of rootfs after building the env
func (e *Env) RootfsSizeMB() int64 {
	return e.rootfsSize >> 20
}

func (e *Env) Build(ctx context.Context, tracer trace.Tracer, postProcessor *writer.PostProcessor, docker *client.Client, legacyDocker *docker.Client) error {
	childCtx, childSpan := tracer.Start(ctx, "build")
	defer childSpan.End()

	err := os.MkdirAll(e.BuildDir(), 0o777)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, "error initializing directories for building env", err)

		return fmt.Errorf("error initializing directories for building env: %w", err)
	}

	rootfs, err := NewRootfs(childCtx, tracer, postProcessor, e, docker, legacyDocker)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, "error creating rootfs for env", err)

		return fmt.Errorf("error creating rootfs for env: %w", err)
	}

	network, err := NewFCNetwork(childCtx, tracer, postProcessor, e)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, "error network setup for FC while building env", err)

		return fmt.Errorf("error network setup for FC while building env: %w", err)
	}

	defer network.Cleanup(childCtx, tracer)

	_, err = NewSnapshot(childCtx, tracer, postProcessor, e, network, rootfs)
	if err != nil {
		telemetry.ReportCriticalError(childCtx, "error snapshot for env", err)

		return fmt.Errorf("error snapshot for env: %w", err)
	}

	return nil
}

func (e *Env) Remove(ctx context.Context, tracer trace.Tracer) error {
	childCtx, childSpan := tracer.Start(ctx, "move-to-env-dir")
	defer childSpan.End()

	err := os.RemoveAll(e.BuildDir())
	if err != nil {
		telemetry.ReportCriticalError(childCtx, "error removing build dir", err)

		return fmt.Errorf("error removing build dir: %w", err)
	}

	telemetry.ReportEvent(childCtx, "removed build dir")

	return nil
}
