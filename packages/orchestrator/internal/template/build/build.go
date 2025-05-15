package build

import (
	"context"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	templatelocal "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

//go:embed provision.sh
var provisionEnvScriptFile string
var EnvInstanceTemplate = template.Must(template.New("provisioning-script").Parse(provisionEnvScriptFile))

func Build(
	ctx context.Context,
	tracer trace.Tracer,
	templateConfig *TemplateConfig,
	postProcessor *writer.PostProcessor,
	docker *client.Client,
	legacyDocker *docker.Client,
	templateCacheFiles *storage.TemplateCacheFiles,
	templateBuildDir string,
	rootfsPath string,
) (s *templatelocal.LocalTemplate, e error) {
	childCtx, childSpan := tracer.Start(ctx, "template-build")
	defer childSpan.End()

	// Create a rootfs file
	err := NewRootfs(childCtx, tracer, postProcessor, templateConfig, docker, legacyDocker, rootfsPath)
	if err != nil {
		return nil, fmt.Errorf("error creating rootfs for template '%s' during build '%s': %w", templateConfig.TemplateId, templateConfig.BuildId, err)
	}

	buildIDParsed, err := uuid.Parse(templateConfig.BuildId)
	if err != nil {
		return nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	rootfs, err := block.NewLocal(rootfsPath, templateConfig.RootfsBlockSize(), buildIDParsed)
	if err != nil {
		return nil, fmt.Errorf("error reading rootfs blocks: %w", err)
	}

	// Create empty memfile
	memfilePath, err := NewMemory(templateBuildDir, templateConfig.MemoryMB)
	if err != nil {
		return nil, fmt.Errorf("error creating memfile: %w", err)
	}

	memfile, err := block.NewLocal(memfilePath, templateConfig.MemfilePageSize(), buildIDParsed)
	if err != nil {
		return nil, fmt.Errorf("error creating memfile blocks: %w", err)
	}

	return templatelocal.NewLocalTemplate(templateCacheFiles, rootfs, memfile)
}
