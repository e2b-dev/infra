package ioc

import (
	"context"
	"net"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/server"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	tmplserver "github.com/e2b-dev/infra/packages/orchestrator/internal/template/server"
	"github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	templatemanager "github.com/e2b-dev/infra/packages/shared/pkg/grpc/template-manager"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
	"github.com/soheilhy/cmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func NewGRPCCMUXServer(
	lc fx.Lifecycle,
	grpcServer *grpc.Server,
	cmuxServer cmux.CMux,
	logger *zap.Logger,
) net.Listener {
	grpcListener := cmuxServer.Match(cmux.Any()) // the rest are GRPC requests
	lc.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			logger.Info("Starting gRPC server to serve all registered services")
			go func() {
				err := grpcServer.Serve(grpcListener)
				if err != nil {
					logger.Error("gRPC server error", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			logger.Info("Shutting down grpc server")
			grpcServer.GracefulStop()
			return nil
		},
	})
	return grpcListener
}

func NewCMUXServer(
	config cfg.Config,
	globalLogger *zap.Logger,
) cmux.CMux {
	// cmux server, allows us to reuse the same TCP port between grpc and HTTP requests
	cmuxServer, err := factories.NewCMUXServer(context.Background(), config.GRPCPort)
	if err != nil {
		globalLogger.Fatal("failed to create cmux server", zap.Error(err))
	}

	return cmuxServer
}

func NewInfoService(
	sandboxes *sandbox.Map,
	serviceInfo *service.ServiceInfo,
	globalLogger *zap.Logger,
) *service.Server {
	s := service.NewInfoService(serviceInfo, sandboxes)
	globalLogger.Info("Registered gRPC service", zap.String("service", "orchestrator_info.InfoService"))
	return s
}

func NewGRPCServer(
	tel *telemetry.Client,
	orchestratorService *server.Server,
	globalLogger *zap.Logger,
	healthService *health.Server,
	infoService *service.Server,
	tmpl *tmplserver.ServerStore,
) *grpc.Server {
	s := factories.NewGRPCServer(tel)

	grpc_health_v1.RegisterHealthServer(s, healthService)
	orchestratorinfo.RegisterInfoServiceServer(s, infoService)
	orchestrator.RegisterSandboxServiceServer(s, orchestratorService)
	if tmpl != nil {
		templatemanager.RegisterTemplateServiceServer(s, tmpl)
	}

	globalLogger.Info("Registered gRPC service", zap.String("service", "orchestrator.SandboxService"))
	return s
}
