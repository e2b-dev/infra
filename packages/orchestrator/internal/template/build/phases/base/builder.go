package base

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	globalconfig "github.com/e2b-dev/infra/packages/orchestrator/internal/config"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
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
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/metadata"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
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

var tracer = otel.Tracer("github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/phases/base")

type BaseBuilder struct {
	buildcontext.BuildContext

	logger *zap.Logger
	proxy  *proxy.SandboxProxy

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
	proxy *proxy.SandboxProxy,
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
		proxy:  proxy,

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
			tag, err := bb.artifactRegistry.GetTag(ctx, bb.Config.TemplateID, bb.Template.BuildID)
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
	userLogger *zap.Logger,
	_ phases.LayerResult,
	currentLayer phases.LayerResult,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "build base", trace.WithAttributes(
		attribute.String("hash", currentLayer.Hash),
	))
	defer span.End()

	baseMetadata, err := bb.buildLayerFromOCI(
		ctx,
		userLogger,
		currentLayer.Metadata,
		currentLayer.Hash,
	)
	if err != nil {
		return phases.LayerResult{}, phases.NewPhaseBuildError(bb, err)
	}

	return phases.LayerResult{
		Metadata: baseMetadata,
		Cached:   false,
		Hash:     currentLayer.Hash,
	}, nil
}

func (bb *BaseBuilder) buildLayerFromOCI(
	ctx context.Context,
	userLogger *zap.Logger,
	baseMetadata metadata.Template,
	hash string,
) (metadata.Template, error) {
	templateBuildDir := filepath.Join(templatesDirectory, bb.Template.BuildID)
	err := os.MkdirAll(templateBuildDir, 0o777)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error creating template build directory: %w", err)
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
		userLogger,
		bb.BuildContext,
		baseMetadata.Template.BuildID,
		bb.artifactRegistry,
		templateBuildDir,
		rootfsPath,
	)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error building environment: %w", err)
	}

	// Env variables from the Docker image
	baseMetadata.Context.EnvVars = oci.ParseEnvs(envsImg.Env)

	cacheFiles, err := baseMetadata.Template.CacheFiles()
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error creating template files: %w", err)
	}
	localTemplate := sbxtemplate.NewLocalTemplate(cacheFiles, rootfs, memfile)
	defer localTemplate.Close()

	// Provision sandbox with systemd and other vital parts
	userLogger.Info("Provisioning sandbox template")
	// Just a symlink to the rootfs build file, so when the COW cache deletes the underlying file (here symlink),
	// it will not delete the rootfs file. We use the rootfs again later on to start the sandbox template.
	rootfsProvisionPath := filepath.Join(templateBuildDir, rootfsProvisionLink)
	err = os.Symlink(rootfsPath, rootfsProvisionPath)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error creating provision rootfs: %w", err)
	}

	// Allow sandbox internet access during provisioning
	allowInternetAccess := true

	baseSbxConfig := sandbox.Config{
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
		userLogger,
		baseSbxConfig,
		sandbox.RuntimeMetadata{
			TemplateID:  bb.Config.TemplateID,
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
		return metadata.Template{}, fmt.Errorf("error provisioning sandbox: %w", err)
	}

	// Check the rootfs filesystem corruption
	ext4Check, err := filesystem.CheckIntegrity(rootfsPath, true)
	if err != nil {
		zap.L().Error("provisioned filesystem ext4 integrity",
			zap.String("result", ext4Check),
			zap.Error(err),
		)
		return metadata.Template{}, fmt.Errorf("error checking provisioned filesystem integrity: %w", err)
	}
	zap.L().Debug("provisioned filesystem ext4 integrity",
		zap.String("result", ext4Check),
	)

	err = bb.enlargeDiskAfterProvisioning(ctx, bb.Config, rootfs)
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error enlarging disk after provisioning: %w", err)
	}

	// Create sandbox for building template
	userLogger.Debug("Creating base sandbox template layer")

	// TODO: Temporarily set this based on global config, should be removed later (it should be passed as a parameter in build)
	baseSbxConfig.AllowInternetAccess = &globalconfig.AllowSandboxInternet

	sandboxCreator := layer.NewCreateSandboxFromCache(
		baseSbxConfig,
		baseLayerTimeout,
		fc.FirecrackerVersions{
			KernelVersion:      bb.Template.KernelVersion,
			FirecrackerVersion: bb.Template.FirecrackerVersion,
		},
		rootfsPath,
	)

	actionExecutor := layer.NewFunctionAction(func(ctx context.Context, sbx *sandbox.Sandbox, meta metadata.Template) (metadata.Template, error) {
		err = sandboxtools.SyncChangesToDisk(
			ctx,
			bb.proxy,
			sbx.Runtime.SandboxID,
		)
		if err != nil {
			return metadata.Template{}, fmt.Errorf("error running sync command: %w", err)
		}
		return meta, nil
	})

	templateProvider := layer.NewDirectSourceTemplateProvider(localTemplate)

	baseLayer, err := bb.layerExecutor.BuildLayer(ctx, userLogger, layer.LayerBuildCommand{
		SourceTemplate: templateProvider,
		CurrentLayer:   baseMetadata,
		Hash:           hash,
		UpdateEnvd:     false,
		SandboxCreator: sandboxCreator,
		ActionExecutor: actionExecutor,
	})
	if err != nil {
		return metadata.Template{}, fmt.Errorf("error building base layer: %w", err)
	}

	return baseLayer, nil
}

func (bb *BaseBuilder) Layer(
	ctx context.Context,
	_ phases.LayerResult,
	hash string,
) (phases.LayerResult, error) {
	ctx, span := tracer.Start(ctx, "compute base", trace.WithAttributes(
		attribute.String("hash", hash),
	))
	defer span.End()

	switch {
	case bb.Config.FromTemplate != nil:
		sourceMeta := metadata.FromTemplate{
			Alias:   bb.Config.FromTemplate.GetAlias(),
			BuildID: bb.Config.FromTemplate.BuildID,
		}

		// If the template is built from another template, use its metadata
		tm, err := bb.index.Cached(ctx, bb.Config.FromTemplate.BuildID)
		if err != nil {
			return phases.LayerResult{}, fmt.Errorf("error getting base layer from cache, you may need to rebuild the base template: %w", err)
		}

		// From template is always cached, never needs to be built
		return phases.LayerResult{
			Metadata: tm.BasedOn(sourceMeta),
			Hash:     hash,
			Cached:   true,
		}, nil
	default:
		cmdMeta := metadata.Context{
			User:    defaultUser,
			WorkDir: nil,
			EnvVars: make(map[string]string),
		}

		// This is a compatibility for v1 template builds
		if bb.IsV1Build {
			cwd := "/home/user"
			cmdMeta.WorkDir = &cwd
		}

		meta := metadata.Template{
			Version: metadata.CurrentVersion,
			Template: storage.TemplateFiles{
				BuildID:            uuid.New().String(),
				KernelVersion:      bb.Template.KernelVersion,
				FirecrackerVersion: bb.Template.FirecrackerVersion,
			},
			Context:      cmdMeta,
			FromImage:    &bb.Config.FromImage,
			FromTemplate: nil,
			Start:        nil,
		}

		notCachedResult := phases.LayerResult{
			Metadata: meta,
			Cached:   false,
			Hash:     hash,
		}

		// Invalidate base cache
		if bb.Config.Force != nil && *bb.Config.Force {
			return notCachedResult, nil
		}

		bm, err := bb.index.LayerMetaFromHash(ctx, hash)
		if err != nil {
			bb.logger.Info("base layer not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))

			return notCachedResult, nil
		} else {
			meta, err := bb.index.Cached(ctx, bm.Template.BuildID)
			if err != nil {
				zap.L().Info("base layer metadata not found in cache, building new base layer", zap.Error(err), zap.String("hash", hash))

				return notCachedResult, nil
			}
			return phases.LayerResult{
				Metadata: meta,
				Cached:   true,
				Hash:     hash,
			}, nil
		}
	}
}
