package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/nbd"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/network"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type ServerStore struct {
	templatemanager.UnimplementedTemplateServiceServer
	tracer            trace.Tracer
	logger            *zap.Logger
	builder           *build.Builder
	buildCache        *cache.BuildCache
	buildLogger       *zap.Logger
	artifactsregistry artifactsregistry.ArtifactsRegistry
	templateStorage   storage.StorageProvider
	buildStorage      storage.StorageProvider

	wg   *sync.WaitGroup // wait group for running builds
	info *service.ServiceInfo
}

func New(
	ctx context.Context,
	tracer trace.Tracer,
	meterProvider metric.MeterProvider,
	logger *zap.Logger,
	buildLogger *zap.Logger,
	grpc *grpcserver.GRPCServer,
	networkPool *network.Pool,
	devicePool *nbd.DevicePool,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	templateCache *sbxtemplate.Cache,
	templatePersistence storage.StorageProvider,
	limiter *limit.Limiter,
	info *service.ServiceInfo,
) (*ServerStore, error) {
	logger.Info("Initializing template manager")

	artifactsregistry, err := artifactsregistry.GetArtifactsRegistryProvider()
	if err != nil {
		return nil, fmt.Errorf("error getting artifacts registry provider: %v", err)
	}

	buildPersistance, err := storage.GetBuildCacheStorageProvider(ctx, limiter)
	if err != nil {
		return nil, fmt.Errorf("error getting build cache storage provider: %v", err)
	}

	buildCache := cache.NewBuildCache(meterProvider)
	builder := build.NewBuilder(
		logger,
		tracer,
		templatePersistence,
		buildPersistance,
		artifactsregistry,
		devicePool,
		networkPool,
		proxy,
		sandboxes,
		templateCache,
	)

	store := &ServerStore{
		tracer:            tracer,
		logger:            logger,
		builder:           builder,
		buildCache:        buildCache,
		buildLogger:       buildLogger,
		artifactsregistry: artifactsregistry,
		templateStorage:   templatePersistence,
		buildStorage:      buildPersistance,
		info:              info,
		wg:                &sync.WaitGroup{},
	}

	templatemanager.RegisterTemplateServiceServer(grpc.GRPCServer(), store)

	return store, nil
}

func (s *ServerStore) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.New("force exit, not waiting for builds to finish")
	default:
		// Wait for draining state to propagate to all consumers
		if !env.IsLocal() {
			time.Sleep(5 * time.Second)
		}

		s.logger.Info("Waiting for all build jobs to finish")
		s.wg.Wait()

		if !env.IsLocal() {
			// Give some time so all connected services can check build status
			s.logger.Info("Waiting before shutting template builder down server")
			time.Sleep(15 * time.Second)
		}

		s.logger.Info("Template builder shutdown complete")
		return nil
	}
}
