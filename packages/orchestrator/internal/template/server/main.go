package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
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
	featureflags "github.com/e2b-dev/infra/packages/shared/pkg/feature-flags"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/limit"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
)

type closeable interface {
	Close() error
}

type ServerStore struct {
	templatemanager.UnimplementedTemplateServiceServer

	logger            logger.Logger
	builder           *build.Builder
	buildCache        *cache.BuildCache
	buildLogger       logger.Logger
	artifactsregistry artifactsregistry.ArtifactsRegistry
	templateStorage   storage.StorageProvider
	buildStorage      storage.StorageProvider

	wg   *sync.WaitGroup // wait group for running builds
	info *service.ServiceInfo

	closers []closeable
}

func New(
	ctx context.Context,
	config cfg.Config,
	featureFlags *featureflags.Client,
	meterProvider metric.MeterProvider,
	logger logger.Logger,
	buildLogger logger.Logger,
	sandboxFactory *sandbox.Factory,
	proxy *proxy.SandboxProxy,
	sandboxes *sandbox.Map,
	templateCache *sbxtemplate.Cache,
	templatePersistence storage.StorageProvider,
	limiter *limit.Limiter,
	info *service.ServiceInfo,
) (s *ServerStore, e error) {
	logger.Info(ctx, "Initializing template manager")

	closers := make([]closeable, 0)
	defer func() {
		if e == nil {
			return
		}

		for _, closer := range closers {
			if err := closer.Close(); err != nil {
				logger.Error(ctx, "error closing resource", zap.Error(err))
			}
		}
	}()

	artifactsregistry, err := artifactsregistry.GetArtifactsRegistryProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting artifacts registry provider: %w", err)
	}

	dockerhubRepository, err := dockerhub.GetRemoteRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting docker remote repository provider: %w", err)
	}
	closers = append(closers, dockerhubRepository)

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
		config.BuilderConfig,
		logger,
		featureFlags,
		sandboxFactory,
		templatePersistence,
		buildPersistance,
		artifactsregistry,
		dockerhubRepository,
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
		closers:           closers,
	}

	return store, nil
}

func (s *ServerStore) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.New("force exit, not waiting for builds to finish")
	default:
		var closersErr error
		for _, closer := range s.closers {
			err := closer.Close()
			if err != nil {
				closersErr = errors.Join(closersErr, err)
			}
		}
		if closersErr != nil {
			return fmt.Errorf("failed to close services: %w", closersErr)
		}

		return nil
	}
}

func (s *ServerStore) Wait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.New("force exit, not waiting for builds to finish")
	default:
		s.logger.Info(ctx, "Waiting for all build jobs to finish")
		s.wg.Wait()

		if !env.IsLocal() {
			s.logger.Info(ctx, "Waiting for consumers to check build status")
			time.Sleep(15 * time.Second)
		}

		s.logger.Info(ctx, "Template build queue cleaned")

		return nil
	}
}
