package server

import (
	"context"

	artifactregistry "cloud.google.com/go/artifactregistry/apiv1"
	"github.com/docker/docker/client"
	docker "github.com/fsouza/go-dockerclient"
	grpc_zap "github.com/grpc-ecosystem/go-grpc-middleware/logging/zap"
	"github.com/grpc-ecosystem/go-grpc-middleware/v2/interceptors/recovery"
	"go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

	e2bgrpc "github.com/e2b-dev/infra/packages/shared/pkg/grpc"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
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

	opts := []grpc_zap.Option{grpc_zap.WithLevels(grpc_zap.DefaultCodeToLevel)}

	s := grpc.NewServer(
		grpc.StatsHandler(e2bgrpc.NewStatsWrapper(otelgrpc.NewServerHandler())),
		grpc.ChainUnaryInterceptor(
			grpc_zap.UnaryServerInterceptor(logger, opts...),
			recovery.UnaryServerInterceptor(),
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
