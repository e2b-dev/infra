package base

import (
	"context"
	"errors"
	"fmt"

	containerregistry "github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/block"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/rootfs"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func constructLayerFilesFromOCI(
	ctx context.Context,
	userLogger logger.Logger,
	buildContext buildcontext.BuildContext,
	phaseMetadata phases.PhaseMeta,
	// The base build ID can be different from the final requested template build ID.
	baseBuildID string,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	dockerhubRepository dockerhub.RemoteRepository,
	rootfsPath string,
) (r *block.Local, m block.ReadonlyDevice, c containerregistry.Config, e error) {
	ctx, span := tracer.Start(ctx, "template-build")
	defer span.End()

	// Create a rootfs file
	rtfs := rootfs.New(
		artifactRegistry,
		dockerhubRepository,
		buildContext,
	)
	provisionScript, err := getProvisionScript(ctx, ProvisionScriptParams{
		BusyBox:           "/" + rootfs.BusyBoxPath,
		ResultPath:        provisionScriptResultPath,
		MirrorSetupScript: GetMirrorSetupScript(),
	})
	if err != nil {
		return nil, nil, containerregistry.Config{}, fmt.Errorf("error getting provision script: %w", err)
	}
	imgConfig, err := rtfs.CreateExt4Filesystem(ctx, userLogger, phaseMetadata, rootfsPath, provisionScript, provisionLogPrefix, provisionScriptResultPath)
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
		err := errors.Join(err, rootfs.Close())

		return nil, nil, containerregistry.Config{}, fmt.Errorf("error creating memfile: %w", err)
	}

	return rootfs, memfile, imgConfig, nil
}
