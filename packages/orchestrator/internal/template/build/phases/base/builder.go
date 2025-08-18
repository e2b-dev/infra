package base

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/fc"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/buildcontext"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/filesystem"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/core/oci"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/layer"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/sandboxtools"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/storage/cache"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/constants"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/id"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

const (
	templatesDirectory = "/orchestrator/build-templates"

	rootfsBuildFileName = "rootfs.filesystem.build"
	rootfsProvisionLink = "rootfs.filesystem.build.provision"

	baseLayerTimeout = 10 * time.Minute
	waitEnvdTimeout  = 60 * time.Second

	defaultUser = "root"
)

type BaseBuilder struct {
	buildcontext.BuildContext

	logger *zap.Logger
	tracer trace.Tracer

	templateStorage  storage.StorageProvider
	devicePool       *nbd.DevicePool
	networkPool      *network.Pool
	artifactRegistry artifactsregistry.ArtifactsRegistry

	layerExecutor *layer.LayerExecutor
	index         cache.Index
	metrics       *metrics.BuildMetrics
}

func New(
	buildContext buildcontext.BuildContext,
	logger *zap.Logger,
	tracer trace.Tracer,
	templateStorage storage.StorageProvider,
	devicePool *nbd.DevicePool,
	networkPool *network.Pool,
	artifactRegistry artifactsregistry.ArtifactsRegistry,
	layerExecutor *layer.LayerExecutor,
	index cache.Index,
	metrics *metrics.BuildMetrics,
) *BaseBuilder {
	return &BaseBuilder{
		BuildContext: buildContext,

		logger: logger,
		tracer: tracer,

		templateStorage:  templateStorage,
		devicePool:       devicePool,
		networkPool:      networkPool,
		artifactRegistry: artifactRegistry,

		layerExecutor: layerExecutor,
		index:         index,
		metrics:       metrics,
	}
}

func (bb *BaseBuilder) Prefix() string {
	return "base"
}

func (bb *BaseBuilder) String(ctx context.Context) (string, error) {
	var baseSource string
	if bb.Config.FromTemplate != nil {
		baseSource = "FROM TEMPLATE " + bb.Config.FromTemplate.GetAlias()
	} else {
		fromImage := bb.Config.FromImage
		if fromImage == "" {
			tag, err := bb.artifactRegistry.GetTag(ctx, bb.Template.TemplateID, bb.Template.BuildID)
			if err != nil {
				return "", fmt.Errorf("error getting tag for template: %w", err)
			}
			fromImage = tag
		}
		baseSource = "FROM " + fromImage
	}

	return baseSource, nil
}

func (bb *BaseBuilder) Metadata() phases.PhaseMeta {
	return phases.PhaseMeta{
		Phase:    metrics.PhaseBase,
		StepType: "base",
	}
}

func (bb *BaseBuilder) Build(
	ctx context.Context,
	_ phases.LayerResult,
	currentLayer phases.LayerResult,
	_ string,
) (phases.LayerResult, error) {
	baseMetadata, err := bb.buildLayerFromOCI(
		ctx,
		currentLayer.Metadata,
		currentLayer.Hash,
	)
	if err != nil {
		return phases.LayerResult{}, &phases.PhaseBuildError{
			Phase: string(metrics.PhaseBase),
			Step:  "base",
			Err:   err,
		}
	}

	return phases.LayerResult{
		Metadata: baseMetadata,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (bb *BaseBuilder) buildLayerFromOCI(
	ctx context.Context,
	baseMetadata cache.LayerMetadata,
	hash string,
) (cache.LayerMetadata, error) {
	templateBuildDir := filepath.Join(templatesDirectory, bb.Template.BuildID)
	err := os.MkdirAll(templateBuildDir, 0o777)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error creating template build directory: %w", err)
	}
	defer func() {
		err := os.RemoveAll(templateBuildDir)
		if err != nil {
			bb.logger.Error("Error while removing template build directory", zap.Error(err))
		}
	}()

	// Created here to be able to pass it to CreateSandbox for populating COW cache
	rootfsPath := filepath.Join(templateBuildDir, rootfsBuildFileName)

	rootfs, memfile, envsImg, err := constructLayerFilesFromOCI(
		ctx,
		bb.tracer,
		bb.BuildContext,
		baseMetadata.Template.BuildID,
		bb.artifactRegistry,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error building environment: %w", err)
	}

	// Env variables from the Docker image
	baseMetadata.CmdMeta.EnvVars = oci.ParseEnvs(envsImg.Env)

	cacheFiles, err := baseMetadata.Template.CacheFiles()
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error creating template files: %w", err)
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
		return cache.LayerMetadata{}, fmt.Errorf("error creating provision rootfs: %w", err)
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
	err = bb.provisionSandbox(
		ctx,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			SandboxID:   config.InstanceBuildPrefix + id.Generate(),
			ExecutionID: uuid.NewString(),
		},
		fc.FirecrackerVersions{
			KernelVersion:      bb.Template.KernelVersion,
			FirecrackerVersion: bb.Template.FirecrackerVersion,
		},
		localTemplate,
		rootfsProvisionPath,
		provisionScriptResultPath,
		provisionLogPrefix,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error provisioning sandbox: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := filesystem.CheckIntegrity(rootfsPath, true)
	if err != nil {
		zap.L().Error("provisioned filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		return cache.LayerMetadata{}, fmt.Errorf("error checking provisioned filesystem integrity: %w", err)
	}
	zap.L().Debug("provisioned filesystem ext4 integrity",
		zap.String("result", ext4Check),
	)

	err = bb.enlargeDiskAfterProvisioning(ctx, bb.Config, rootfs)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error enlarging disk after provisioning: %w", err)
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
		fc.FirecrackerVersions{
			KernelVersion:      bb.Template.KernelVersion,
			FirecrackerVersion: bb.Template.FirecrackerVersion,
		},
		localTemplate,
		baseLayerTimeout,
		rootfsPath,
		fc.ProcessOptions{
			InitScriptPath:      constants.SystemdInitPath,
			KernelLogs:          env.IsDevelopment(),
			SystemdToKernelLogs: false,
		},
		nil,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error creating sandbox: %w", err)
	}
	defer sourceSbx.Stop(ctx)

	err = sourceSbx.WaitForEnvd(
		ctx,
		bb.tracer,
		waitEnvdTimeout,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("failed to wait for sandbox start: %w", err)
	}

	err = bb.layerExecutor.PauseAndUpload(
		ctx,
		sourceSbx,
		hash,
		baseMetadata,
	)
	if err != nil {
		return cache.LayerMetadata{}, fmt.Errorf("error pausing and uploading template: %w", err)
	}

	return baseMetadata, nil
}

func (bb *BaseBuilder) Layer(
	ctx context.Context,
	lastStepResult phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	switch {
	case bb.Config.FromTemplate != nil:
		// If the template is built from another template, use its metadata
		tm, err := metadata.ReadTemplateMetadata(ctx, bb.templateStorage, bb.Config.FromTemplate.BuildID)
		if err != nil {
			return phases.LayerResult{}, fmt.Errorf("error getting base layer from cache, you may need to rebuild the base template: %w", err)
		}

		// From template is always cached, never needs to be built
		return phases.LayerResult{
			Metadata: cache.LayerMetadata{
				Template: tm.Template,
				CmdMeta:  tm.Metadata,
			},
			Hash:   hash,
			Cached: true,
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

		var baseMetadata cache.LayerMetadata
		bm, err := bb.index.LayerMetaFromHash(ctx, hash)
		if err != nil {
			bb.logger.Info("base layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))

			baseMetadata = cache.LayerMetadata{
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
			baseMetadata = cache.LayerMetadata{
				Template: storage.TemplateFiles{
					TemplateID:         id.Generate(),
					BuildID:            uuid.New().String(),
					KernelVersion:      bb.Template.KernelVersion,
					FirecrackerVersion: bb.Template.FirecrackerVersion,
				},
				CmdMeta: cmdMeta,
			}
		}

		baseCached, err := bb.index.IsCached(ctx, baseMetadata)
		if err != nil {
			return phases.LayerResult{}, fmt.Errorf("error checking if base layer is cached: %w", err)
		}

		return phases.LayerResult{
			Metadata: baseMetadata,
			Cached:   baseCached,
			Hash:     hash,
		}, nil
	}
}
