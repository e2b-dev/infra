package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/grpcserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/template/cache"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/smap"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type ServerStore struct {
	templatemanager.UnimplementedTemplateServiceServer

	logger            *zap.Logger
	builder           *build.Builder
	buildCache        *cache.BuildCache
	buildLogger       *zap.Logger
	artifactsregistry artifactsregistry.ArtifactsRegistry
	templateStorage   storage.StorageProvider
	buildStorage      storage.StorageProvider

	wg   *sync.WaitGroup // wait group for running builds
	info *service.ServiceInfo

	_close func() error
}

func New(
	ctx context.Context,
	meterProvider metric.MeterProvider,
	logger *zap.Logger,
	buildLogger *zap.Logger,
	grpc *grpcserver.GRPCServer,
	sandboxFactory *sandbox.Factory,
	proxy *proxy.SandboxProxy,
	sandboxes *smap.Map[*sandbox.Sandbox],
	templateCache *sbxtemplate.Cache,
	templatePersistence storage.StorageProvider,
	limiter *limit.Limiter,
	info *service.ServiceInfo,
) (s *ServerStore, e error) {
	logger.Info("Initializing template manager")

	artifactsregistry, err := artifactsregistry.GetArtifactsRegistryProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting artifacts registry provider: %w", err)
	}

	dockerhubRepository, err := dockerhub.GetRemoteRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting docker remote repository provider: %w", err)
	}
	defer func() {
		if e == nil {
			return
		}

		if err := dockerhubRepository.Close(); err != nil {
			logger.Error("error closing docker remote repository provider", zap.Error(err))
		}
	}()

	buildPersistance, err := storage.GetBuildCacheStorageProvider(ctx, limiter)
	if err != nil {
		return nil, fmt.Errorf("error getting build cache storage provider: %w", err)
	}

	buildCache := cache.NewBuildCache(meterProvider)
	buildMetrics, err := metrics.NewBuildMetrics(meterProvider)
	if err != nil {
		return nil, fmt.Errorf("failed to create build metrics: %w", err)
	}

	builder := build.NewBuilder(
		logger,
		sandboxFactory,
		templatePersistence,
		buildPersistance,
		artifactsregistry,
		dockerRemoteRepository,
		proxy,
		sandboxes,
		templateCache,
		buildMetrics,
	)

	store := &ServerStore{
		logger:            logger,
		builder:           builder,
		buildCache:        buildCache,
		buildLogger:       buildLogger,
		artifactsregistry: artifactsregistry,
		templateStorage:   templatePersistence,
		buildStorage:      buildPersistance,
		info:              info,
		wg:                &sync.WaitGroup{},
		_close: func() error {
			err := dockerhubRepository.Close()
			if err != nil {
				return fmt.Errorf("failed to close dockerhub repository: %w", err)
			}
			return nil
		},
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
			time.Sleep(15 * time.Second)
		}

		s.logger.Info("Waiting for all build jobs to finish")
		s.wg.Wait()

		if !env.IsLocal() {
			s.logger.Info("Waiting for consumers to check build status")
			time.Sleep(15 * time.Second)
		}

		s.logger.Info("Template build queue cleaned")

		err := s._close()
		if err != nil {
			return fmt.Errorf("failed to close services: %w", err)
		}
		return nil
	}
}
