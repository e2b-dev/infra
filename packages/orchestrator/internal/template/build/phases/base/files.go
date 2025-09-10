package base

import (
	"context"
	_ "embed"
	"fmt"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
)

func constructLayerFilesFromOCI(
	ctx context.Context,
	buildContext buildcontext.BuildContext,
	// The base build ID can be different from the final requested template build ID.
	baseBuildID string,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	templateBuildDir string,
	rootfsPath string,
) (r *block.Local, m block.ReadonlyDevice, c containerregistry.Config, e error) {
	childCtx, childSpan := tracer.Start(ctx, "template-build")
	defer childSpan.End()

	// Create a rootfs file
	rtfs := rootfs.New(
		artifactRegistry,
		buildContext.Template,
		buildContext.Config,
	)
	provisionScript, err := getProvisionScript(ctx, ProvisionScriptParams{
		BusyBox:    "/" + rootfs.BusyBoxPath,
		ResultPath: provisionScriptResultPath,
	})
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error getting provision script: %w", err)
	}
	imgConfig, err := rtfs.CreateExt4Filesystem(childCtx, buildContext.UserLogger, rootfsPath, provisionScript, provisionLogPrefix)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating ext4 filesystem: %w", err)
	}

	buildIDParsed, err := uuid.Parse(baseBuildID)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("failed to parse build id: %w", err)
	}

	rootfs, err := block.NewLocal(rootfsPath, buildContext.Config.RootfsBlockSize(), buildIDParsed)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error reading rootfs blocks: %w", err)
	}

	// Create empty memfile
	memfile, err := block.NewEmpty(
		buildContext.Config.MemoryMB<<constants.ToMBShift,
		config.MemfilePageSize(buildContext.Config.HugePages),
		buildIDParsed,
	)
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating memfile: %w", err)
	}

	return rootfs, memfile, imgConfig, nil
}
