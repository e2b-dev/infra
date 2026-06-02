//go:build linux

package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/pkg/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/proxy"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox"
	sbxtemplate "github.com/e2b-dev/infra/packages/orchestrator/pkg/sandbox/template"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/builderrors"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/cache"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
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
	featureFlags      *featureflags.Client
	artifactsregistry artifactsregistry.ArtifactsRegistry
	templateStorage   storage.StorageProvider
	buildStorage      storage.StorageProvider

	wg           *sync.WaitGroup // wait group for running builds
	activeBuilds atomic.Int64    // counter for active builds (for debugging)
	drainOnce    sync.Once
	drainDone    chan struct{}
	buildStartMu sync.RWMutex

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
	templateCache *sbxtemplate.Cache,
	templatePersistence storage.StorageProvider,
	buildPersistence storage.StorageProvider,
	uploads *sandbox.Uploads,
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

	artifactsRegistry, err := artifactsregistry.GetArtifactsRegistryProvider(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting artifacts registry provider: %w", err)
	}

	dockerhubRepository, err := dockerhub.GetRemoteRepository(ctx)
	if err != nil {
		return nil, fmt.Errorf("error getting docker remote repository provider: %w", err)
	}
	closers = append(closers, dockerhubRepository)

	buildCache := cache.NewBuildCache(ctx, meterProvider)
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
		buildPersistence,
		artifactsRegistry,
		dockerhubRepository,
		proxy,
		sandboxFactory.Sandboxes,
		templateCache,
		buildMetrics,
		uploads,
	)

	store := &ServerStore{
		logger:            logger,
		builder:           builder,
		buildCache:        buildCache,
		buildLogger:       buildLogger,
		featureFlags:      featureFlags,
		artifactsregistry: artifactsRegistry,
		templateStorage:   templatePersistence,
		buildStorage:      buildPersistence,
		wg:                &sync.WaitGroup{},
		drainDone:         make(chan struct{}),
		closers:           closers,
	}

	return store, nil
}

func (s *ServerStore) Close(ctx context.Context) error {
	s.StartDraining(ctx)

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

func (s *ServerStore) Wait(ctx context.Context, forced bool) error {
	s.StartDraining(ctx)
	logCtx := ctx
	if forced {
		logCtx = context.WithoutCancel(ctx)
		s.cancelRunningBuilds(logCtx)
	}

	if err := s.waitBuildStarts(ctx); err != nil {
		return err
	}
	if forced {
		s.cancelRunningBuilds(logCtx)
	}

	s.logger.Info(logCtx, "Waiting for all build jobs to finish", zap.Int64("active_builds", s.activeBuilds.Load()))
	if err := waitBuildGroup(ctx, s.wg); err != nil {
		return err
	}

	if !forced && !env.IsLocal() {
		s.logger.Info(logCtx, "Waiting for consumers to check build status")
		time.Sleep(15 * time.Second)
	}

	s.logger.Info(logCtx, "Template build queue cleaned")

	return nil
}

func waitBuildGroup(ctx context.Context, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		return nil
	default:
	}

	select {
	case <-ctx.Done():
		return fmt.Errorf("waiting for template builds: %w", ctx.Err())
	case <-done:
		return nil
	}
}

func (s *ServerStore) cancelRunningBuilds(ctx context.Context) {
	if s.buildCache == nil {
		return
	}

	canceled := s.buildCache.FailRunning(&templatemanager.TemplateBuildStatusReason{
		Message: builderrors.ErrCanceled.Error(),
	})
	if canceled == 0 {
		return
	}

	s.logger.Info(ctx, "canceled running template builds during forced drain", zap.Int("canceled_builds", canceled))
}
