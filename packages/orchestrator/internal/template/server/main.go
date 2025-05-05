package server

import (
	"context"
	"errors"
	"sync"
	"time"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/storage"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/cache"
	"github.com/e2b-dev/infra/packages/template-manager/internal/constants"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

type ServerStore struct {
	templatemanager.UnimplementedTemplateServiceServer
	tracer           trace.Tracer
	logger           *zap.Logger
	builder          *build.TemplateBuilder
	buildCache       *cache.BuildCache
	buildLogger      *zap.Logger
	templateStorage  *template.Storage
	artifactRegistry *artifactregistry.Client
	healthStatus     templatemanager.HealthState
	wg               *sync.WaitGroup // wait group for running builds
}

func New(ctx context.Context, grpc *grpcserver.GRPCServer, logger *zap.Logger, buildLogger *zap.Logger, tracer trace.Tracer) *ServerStore {
	// Template Manager Initialization
	if err := constants.CheckRequired(); err != nil {
		log.Fatalf("Validation for environment variables failed: %v", err)
	}

	logger.Info("Initializing template manager")

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		panic(err)
	}

	legacyClient, err := docker.NewClientFromEnv()
	if err != nil {
		panic(err)
	}

	artifactRegistry, err := artifactregistry.NewClient(ctx)
	if err != nil {
		panic(err)
	}

	persistence, err := storage.GetTemplateStorageProvider(ctx)
	if err != nil {
		panic(err)
	}

	templateStorage := template.NewStorage(persistence)
	buildCache := cache.NewBuildCache()
	builder := build.NewBuilder(logger, buildLogger, tracer, dockerClient, legacyClient, templateStorage, buildCache, persistence)
	store := &ServerStore{
		tracer:           tracer,
		logger:           logger,
		builder:          builder,
		buildCache:       buildCache,
		buildLogger:      buildLogger,
		artifactRegistry: artifactRegistry,
		templateStorage:  templateStorage,
		healthStatus:     templatemanager.HealthState_Healthy,
		wg:               &sync.WaitGroup{},
	}

	templatemanager.RegisterTemplateServiceServer(grpc.GRPCServer(), store)

	return store
}

func (s *ServerStore) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.New("context canceled during server graceful shutdown")
	default:
		// no new jobs should be started
		s.logger.Info("marking service as draining")
		s.healthStatus = templatemanager.HealthState_Draining
		if !env.IsLocal() {
			time.Sleep(5 * time.Second)
		}

		// wait for all builds to finish
		s.logger.Info("waiting for all jobs to finish")
		s.wg.Wait()

		if !env.IsLocal() {
			// give some time so all connected services can check build status
			s.logger.Info("waiting before shutting down server")
			time.Sleep(15 * time.Second)
		}
		return nil
	}
}
