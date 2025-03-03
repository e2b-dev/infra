package server

import (
	"context"
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

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/template-manager/internal/constants"
	"github.com/e2b-dev/infra/packages/template-manager/internal/template"
)

type serverStore struct {
	templatemanager.UnimplementedTemplateServiceServer
	server             *grpc.Server
	tracer             trace.Tracer
	dockerClient       *client.Client
	legacyDockerClient *docker.Client
	artifactRegistry   *artifactregistry.Client
	templateStorage    *template.Storage
}

func New(logger *zap.Logger) *grpc.Server {
	ctx := context.Background()
	logger.Info("Initializing template manager")

	opts := []logging.Option{
		logging.WithLogOnEvents(logging.StartCall, logging.PayloadReceived, logging.PayloadSent, logging.FinishCall),
		logging.WithLevels(logging.DefaultServerCodeToLevel),
		logging.WithFieldsFromContext(logging.ExtractFields),
	}

	s := grpc.NewServer(
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

	templatemanager.RegisterTemplateServiceServer(s, &serverStore{
		tracer:             otel.Tracer(constants.ServiceName),
		dockerClient:       dockerClient,
		legacyDockerClient: legacyClient,
		artifactRegistry:   artifactRegistry,
		templateStorage:    templateStorage,
	})

	grpc_health_v1.RegisterHealthServer(s, health.NewServer())
	return s
}
