package build

import (
	"context"
	_ "embed"
	"fmt"
	"text/template"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artefactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artefacts-registry"
)

//go:embed provision.sh
var provisionScriptFile string
var ProvisionScriptTemplate = template.Must(template.New("provisioning-script").Parse(provisionScriptFile))

//go:embed configure.sh
var configureScriptFile string
var ConfigureScriptTemplate = template.Must(template.New("provisioning-finish-script").Parse(configureScriptFile))

func Build(
	ctx context.Context,
	tracer trace.Tracer,
	templateConfig *TemplateConfig,
	postProcessor *writer.PostProcessor,
	artifactRegistry artefactsregistry.ArtefactsRegistry,
	templateBuildDir string,
	rootfsPath string,
) (r *block.Local, m *block.Local, e error) {
	childCtx, childSpan := tracer.Start(ctx, "template-build")
	defer childSpan.End()

	// Create a rootfs file
	rtfs := NewRootfs(templateConfig, artifactRegistry)
	err := rtfs.createExt4Filesystem(childCtx, tracer, postProcessor, rootfsPath)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating rootfs for template '%s' during build '%s': %w", templateConfig.TemplateId, templateConfig.BuildId, err)
	}

	buildIDParsed, err := uuid.Parse(templateConfig.BuildId)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse build id: %w", err)
	}

	rootfs, err := block.NewLocal(rootfsPath, templateConfig.RootfsBlockSize(), buildIDParsed)
	if err != nil {
		return nil, nil, fmt.Errorf("error reading rootfs blocks: %w", err)
	}

	// Create empty memfile
	memfilePath, err := NewMemory(templateBuildDir, templateConfig.MemoryMB)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating memfile: %w", err)
	}

	memfile, err := block.NewLocal(memfilePath, templateConfig.MemfilePageSize(), buildIDParsed)
	if err != nil {
		return nil, nil, fmt.Errorf("error creating memfile blocks: %w", err)
	}

	return rootfs, memfile, nil
}
