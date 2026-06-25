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
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/build/metrics"
	"github.com/e2b-dev/infra/packages/orchestrator/pkg/template/cache"
	artifactsregistry "github.com/e2b-dev/infra/packages/shared/pkg/artifacts-registry"
	"github.com/e2b-dev/infra/packages/shared/pkg/dockerhub"
	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/shared/pkg/featureflags"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
)

type closeable interface {
	Close() error
}

// consumerStatusCheckGracePeriod is how long the graceful drain waits, after
// in-flight builds finish, for consumers to read the final build status before
// the server shuts down.
const consumerStatusCheckGracePeriod = 15 * time.Second

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

// Wait gracefully drains in-flight template builds during shutdown. It waits
// for running builds to finish, bounded by ctx, then gives consumers a grace
// period to read the final build status. It returns ctx.Err() if ctx is
// cancelled before the drain completes.
func (s *ServerStore) Wait(ctx context.Context) error {
	s.logger.Info(ctx, "Waiting for all build jobs to finish", zap.Int64("active_builds", s.activeBuilds.Load()))
	if err := utils.WaitGroupWait(ctx, s.wg); err != nil {
		return fmt.Errorf("waiting for template builds: %w", err)
	}

	if !env.IsLocal() {
		s.logger.Info(ctx, "Waiting for consumers to check build status")
		select {
		case <-time.After(consumerStatusCheckGracePeriod):
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	s.logger.Info(ctx, "Template build queue cleaned")

	return nil
}
