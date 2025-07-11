package build

import (
	"context"
	_ "embed"
	"fmt"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/memory"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/writer"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

func constructBaseLayerFiles(
	ctx context.Context,
	tracer trace.Tracer,
	metadata storage.TemplateFiles,
	buildID string,
	templateConfig config.TemplateConfig,
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

	// Create a rootfs file
	rtfs := rootfs.New(
		artifactRegistry,
		metadata,
		templateConfig,
	)
	provisionScript, err := getProvisionScript(ctx, ProvisionScriptParams{
		ResultPath: provisionScriptResultPath,
	})
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error getting provision script: %w", err)
	}
	imgConfig, err := rtfs.CreateExt4Filesystem(childCtx, tracer, postProcessor, rootfsPath, provisionScript, provisionLogPrefix)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating rootfs for template '%s' during build '%s': %w", metadata.TemplateID, metadata.BuildID, err)
	}

	buildIDParsed, err := uuid.Parse(buildID)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("failed to parse build id: %w", err)
	}

	rootfs, err := block.NewLocal(rootfsPath, templateConfig.RootfsBlockSize(), buildIDParsed)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error reading rootfs blocks: %w", err)
	}

	// Create empty memfile
	memfilePath, err := memory.NewMemory(templateBuildDir, templateConfig.MemoryMB)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating memfile: %w", err)
	}

	memfile, err := block.NewLocal(memfilePath, templateConfig.MemfilePageSize(), buildIDParsed)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating memfile blocks: %w", err)
	}

	return rootfs, memfile, imgConfig, nil
}
