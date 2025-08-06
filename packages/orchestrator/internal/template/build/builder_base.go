package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/ext4"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type BaseBuilder struct {
	BuildContext

	logger *zap.Logger
	tracer trace.Tracer

	templateStorage  storage.StorageProvider
	buildStorage     storage.StorageProvider
	devicePool       *nbd.DevicePool
	networkPool      *network.Pool
	artifactRegistry artifactsregistry.ArtifactsRegistry

	layerExecutor *LayerExecutor
}

func NewBaseBuilder(
	buildContext BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
	templateStorage storage.StorageProvider,
	buildStorage storage.StorageProvider,
	devicePool *nbd.DevicePool,
	networkPool *network.Pool,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	layerExecutor *LayerExecutor,
) *BaseBuilder {
	return &BaseBuilder{
		BuildContext: buildContext,

		logger: logger,
		tracer: tracer,

		templateStorage:  templateStorage,
		buildStorage:     buildStorage,
		devicePool:       devicePool,
		networkPool:      networkPool,
		artifactRegistry: artifactRegistry,

		layerExecutor: layerExecutor,
	}
}

func (bb *BaseBuilder) Build(
	ctx context.Context,
	_ LayerResult,
) (LayerResult, error) {
	hash, err := hashBase(bb.Config)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error getting base hash: %w", err)
	}

	cached, baseMetadata, err := bb.setup(ctx, hash)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error setting up build: %w", err)
	}

	// Print the base layer information
	var baseSource string
	if bb.Config.FromTemplate != nil {
		baseSource = "FROM TEMPLATE " + bb.Config.FromTemplate.GetAlias()
	} else {
		fromImage := bb.Config.FromImage
		if fromImage == "" {
			tag, err := bb.artifactRegistry.GetTag(ctx, bb.Template.TemplateID, bb.Template.BuildID)
			if err != nil {
				return LayerResult{}, fmt.Errorf("error getting tag for template: %w", err)
			}
			fromImage = tag
		}
		baseSource = "FROM " + fromImage
	}
	bb.UserLogger.Info(layerInfo(cached, "base", baseSource, hash))

	if cached {
		return LayerResult{
			Metadata: baseMetadata,
			Cached:   true,
			Hash:     hash,
		}, nil
	}

	baseMetadata, err = bb.buildBaseLayer(
		ctx,
		baseMetadata,
		hash,
	)
	if err != nil {
		return LayerResult{}, fmt.Errorf("error building base layer: %w", err)
	}

	return LayerResult{
		Metadata: baseMetadata,
		Cached:   false,
		Hash:     hash,
	}, nil
}

func (bb *BaseBuilder) buildBaseLayer(
	ctx context.Context,
	baseMetadata LayerMetadata,
	hash string,
) (LayerMetadata, error) {
	templateBuildDir := filepath.Join(templatesDirectory, bb.Template.BuildID)
	err := os.MkdirAll(templateBuildDir, 0o777)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating template build directory: %w", err)
	}
	defer func() {
		err := os.RemoveAll(templateBuildDir)
		if err != nil {
			bb.logger.Error("Error while removing template build directory", zap.Error(err))
		}
	}()

	// Created here to be able to pass it to CreateSandbox for populating COW cache
	rootfsPath := filepath.Join(templateBuildDir, rootfsBuildFileName)

	rootfs, memfile, envsImg, err := constructBaseLayerFiles(
		ctx,
		bb.tracer,
		bb.BuildContext,
		baseMetadata.Template.BuildID,
		bb.artifactRegistry,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error building environment: %w", err)
	}

	// Env variables from the Docker image
	baseMetadata.CmdMeta.EnvVars = oci.ParseEnvs(envsImg.Env)

	cacheFiles, err := baseMetadata.Template.CacheFiles()
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating template files: %w", err)
	}
	localTemplate := sbxtemplate.NewLocalTemplate(cacheFiles, rootfs, memfile)
	defer localTemplate.Close()

	// Provision sandbox with systemd and other vital parts
	bb.UserLogger.Info("Provisioning sandbox template")
	// Just a symlink to the rootfs build file, so when the COW cache deletes the underlying file (here symlink),
	// it will not delete the rootfs file. We use the rootfs again later on to start the sandbox template.
	rootfsProvisionPath := filepath.Join(templateBuildDir, rootfsProvisionLink)
	err = os.Symlink(rootfsPath, rootfsProvisionPath)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating provision rootfs: %w", err)
	}

	// Allow sandbox internet access during provisioning
	allowInternetAccess := true

	baseSbxConfig := sandbox.Config{
		BaseTemplateID: baseMetadata.Template.TemplateID,

		Vcpu:      bb.Config.VCpuCount,
		RamMB:     bb.Config.MemoryMB,
		HugePages: bb.Config.HugePages,

		AllowInternetAccess: &allowInternetAccess,

		Envd: sandbox.EnvdMetadata{
			Version: bb.EnvdVersion,
		},
	}
	fcVersions := fc.FirecrackerVersions{
		KernelVersion:      bb.Template.KernelVersion,
		FirecrackerVersion: bb.Template.FirecrackerVersion,
	}
	err = bb.provisionSandbox(
		ctx,
		bb.UserLogger,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		fcVersions,
		localTemplate,
		rootfsProvisionPath,
		provisionScriptResultPath,
		provisionLogPrefix,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error provisioning sandbox: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := ext4.CheckIntegrity(rootfsPath, true)
	if err != nil {
		zap.L().Error("provisioned filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		return LayerMetadata{}, fmt.Errorf("error checking provisioned filesystem integrity: %w", err)
	}
	zap.L().Debug("provisioned filesystem ext4 integrity",
		zap.String("result", ext4Check),
	)

	err = bb.enlargeDiskAfterProvisioning(ctx, bb.Config, rootfs)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error enlarging disk after provisioning: %w", err)
	}

	// Create sandbox for building template
	bb.UserLogger.Debug("Creating base sandbox template layer")

	// TODO: Temporarily set this based on global config, should be removed later (it should be passed as a parameter in build)
	baseSbxConfig.AllowInternetAccess = &globalconfig.AllowSandboxInternet
	sourceSbx, err := sandbox.CreateSandbox(
		ctx,
		bb.tracer,
		bb.networkPool,
		bb.devicePool,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		fcVersions,
		localTemplate,
		baseLayerTimeout,
		rootfsPath,
		fc.ProcessOptions{
			InitScriptPath:      systemdInitPath,
			KernelLogs:          env.IsDevelopment(),
			SystemdToKernelLogs: false,
		},
		nil,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error creating sandbox: %w", err)
	}
	defer sourceSbx.Stop(ctx)

	err = sourceSbx.WaitForEnvd(
		ctx,
		bb.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	err = bb.layerExecutor.PauseAndUpload(
		ctx,
		sourceSbx,
		hash,
		baseMetadata,
	)
	if err != nil {
		return LayerMetadata{}, fmt.Errorf("error pausing and uploading template: %w", err)
	}

	return baseMetadata, nil
}

func (bb *BaseBuilder) setup(
	ctx context.Context,
	hash string,
) (bool, LayerMetadata, error) {
	switch {
	case bb.Config.FromTemplate != nil:
		// If the template is built from another template, use its metadata
		tm, err := ReadTemplateMetadata(ctx, bb.templateStorage, bb.Config.FromTemplate.BuildID)
		if err != nil {
			return false, LayerMetadata{}, fmt.Errorf("error getting base layer from cache, you may need to rebuild the base template: %w", err)
		}

		return true, LayerMetadata{
			Template: tm.Template,
			CmdMeta:  tm.Metadata,
		}, nil
	default:
		cmdMeta := sandboxtools.CommandMetadata{
			User:    defaultUser,
			WorkDir: nil,
			EnvVars: make(map[string]string),
		}

		// This is a compatibility for v1 template builds
		if bb.IsV1Build {
			cwd := "/home/user"
			cmdMeta.WorkDir = &cwd
		}

		var baseMetadata LayerMetadata
		bm, err := layerMetaFromHash(ctx, bb.buildStorage, bb.CacheScope, hash)
		if err != nil {
			bb.logger.Info("base layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))

			baseMetadata = LayerMetadata{
				Template: storage.TemplateFiles{
					TemplateID:         id.Generate(),
					BuildID:            uuid.New().String(),
					KernelVersion:      bb.Template.KernelVersion,
					FirecrackerVersion: bb.Template.FirecrackerVersion,
				},
				CmdMeta: cmdMeta,
			}
		} else {
			baseMetadata = bm
		}

		// Invalidate base cache
		if bb.Config.Force != nil && *bb.Config.Force {
			baseMetadata = LayerMetadata{
				Template: storage.TemplateFiles{
					TemplateID:         id.Generate(),
					BuildID:            uuid.New().String(),
					KernelVersion:      bb.Template.KernelVersion,
					FirecrackerVersion: bb.Template.FirecrackerVersion,
				},
				CmdMeta: cmdMeta,
			}
		}

		baseCached, err := isCached(ctx, bb.templateStorage, baseMetadata)
		if err != nil {
			return false, LayerMetadata{}, fmt.Errorf("error checking if base layer is cached: %w", err)
		}

		return baseCached, baseMetadata, nil
	}
}
