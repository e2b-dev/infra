package server

import (
	"context"
	"errors"
	"github.com/docker/docker/client"
	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/template-manager/internal/constants"
	docker "github.com/fsouza/go-dockerclient"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/logging"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/selector"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"google.golang.org/grpc/keepalive"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"

	"github.com/e2b-dev/infra/packages/shared/pkg/env"
	"github.com/e2b-dev/infra/packages/template-manager/internal/build"
	"github.com/e2b-dev/infra/packages/template-manager/internal/cache"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

type ServerStore struct {
	templatemanager.UnimplementedTemplateServiceServer
	server           *grpc.Server
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

func New(logger *zap.Logger, buildLogger *zap.Logger) *grpc.Server {
	ctx := context.Background()

	logger.Info("Initializing template manager")

	opts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
		logging.WithLevels(logging.DefaultServerCodeToLevel),
		logging.WithFieldsFromContext(logging.ExtractFields),
	}

	server := grpc.NewServer(
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second, // Minimum time between pings from client
			PermitWithoutStream: true,            // Allow pings even when no active streams
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    15 * time.Second, // Server sends keepalive pings every 15s
			Timeout: 5 * time.Second,  // Wait 5s for response before considering dead
		}),
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
		grpc.ChainUnaryInterceptor(
			recovery.UnaryServerInterceptor(),
			selector.UnaryServerInterceptor(
				logging.UnaryServerInterceptor(l.GRPCLogger(logger), opts...),
				l.WithoutHealthCheck(),
			),
		),
	)
	dockerClient, err := client.NewClientWithOpts(client.FromEnv)
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

	templateStorage := template.NewStorage(ctx)
	buildCache := cache.NewBuildCache()
	builder := build.NewBuilder(logger, buildLogger, otel.Tracer(constants.ServiceName), dockerClient, legacyClient, templateStorage, buildCache)
	store := &ServerStore{
		tracer:           otel.Tracer(constants.ServiceName),
		logger:           logger,
		builder:          builder,
		buildCache:       buildCache,
		buildLogger:      buildLogger,
		artifactRegistry: artifactRegistry,
		templateStorage:  templateStorage,
		healthStatus:     templatemanager.HealthState_Healthy,
		wg:               &sync.WaitGroup{},
		server:           server,
	}

	templatemanager.RegisterTemplateServiceServer(server, store)
	grpc_health_v1.RegisterHealthServer(server, health.NewServer())

	return server, store
}

func (s *ServerStore) Close(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return errors.New("context canceled during server graceful shutdown")
	default:
		// no new jobs should be started
		zap.L().Info("marking service as draining")
		s.healthStatus = templatemanager.HealthState_Draining
		if !env.IsLocal() {
			time.Sleep(5 * time.Second)
		}

		// wait for all builds to finish
		zap.L().Info("waiting for all jobs to finish")
		s.wg.Wait()

		if !env.IsLocal() {
			// give some time so all connected services can check build status
			zap.L().Info("waiting before shutting down server")
			time.Sleep(15 * time.Second)
		}

		// mark service as unhealthy so now new request will be accepted
		s.server.GracefulStop()
		return nil
	}
}
