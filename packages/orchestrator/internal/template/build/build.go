package build

import (
	"context"
	_ "embed"
	"fmt"
	tt "text/template"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/builder"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/templateconfig"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

//go:embed provision.sh
var provisionScriptFile string
var ProvisionScriptTemplate = tt.Must(tt.New("provisioning-script").Parse(provisionScriptFile))

//go:embed configure.sh
var configureScriptFile string
var ConfigureScriptTemplate = tt.Must(tt.New("provisioning-finish-script").Parse(configureScriptFile))

func Build(
	ctx context.Context,
	tracer trace.Tracer,
	templateConfig *templateconfig.TemplateConfig,
	engineConfig *templatemanager.EngineConfig,
	postProcessor *writer.PostProcessor,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	storage storage.StorageProvider,
	networkPool *network.Pool,
	templateCache *template.Cache,
	devicePool *nbd.DevicePool,
	templateBuildDir string,
	rootfsPath string,
) (r *block.Local, m *block.Local, c containerregistry.Config, e error) {
	childCtx, childSpan := tracer.Start(ctx, "template-build")
	defer childSpan.End()

	b := builder.NewImageBuilder(
		artifactRegistry,
		storage,
		networkPool,
		templateCache,
		devicePool,
		templateConfig,
		engineConfig,
	)

	// Create a rootfs file
	rtfs := NewRootfs(
		artifactRegistry,
		templateConfig,
		b,
	)
	config, err := rtfs.createExt4Filesystem(childCtx, tracer, postProcessor, rootfsPath)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating rootfs for template '%s' during build '%s': %w", templateConfig.TemplateID, templateConfig.BuildID, err)
	}

	buildIDParsed, err := uuid.Parse(templateConfig.BuildID)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("failed to parse build id: %w", err)
	}

	rootfs, err := block.NewLocal(rootfsPath, templateConfig.RootfsBlockSize(), buildIDParsed)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error reading rootfs blocks: %w", err)
	}

	// Create empty memfile
	memfilePath, err := NewMemory(templateBuildDir, templateConfig.MemoryMB)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating memfile: %w", err)
	}

	memfile, err := block.NewLocal(memfilePath, templateConfig.MemfilePageSize(), buildIDParsed)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating memfile blocks: %w", err)
	}

	return rootfs, memfile, config, nil
}
