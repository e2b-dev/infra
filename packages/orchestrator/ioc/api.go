package ioc

import (
	"context"
	"net"

	"github.com/soheilhy/cmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"

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
)

func NewGRPCModule() fx.Option {
	return fx.Module("grpc",
		fx.Provide(
			// create cmux server
			fx.Annotate(
				newCMUXServer,
				fx.OnStart(startCMUXServer),
				fx.OnStop(stopCMUXServer),
			),

			newInfoService,
			newGRPCServer,
		),
		fx.Invoke(
			func(CMUXOut) {},
		),
	)
}

type CMUXOut struct {
	CMUX         cmux.CMux
	GRPCListener net.Listener
	HTTPListener net.Listener
}

func newCMUXServer(config cfg.Config) (CMUXOut, error) {
	// cmux server, allows us to reuse the same TCP port between grpc and HTTP requests
	cmuxServer, err := factories.NewCMUXServer(context.Background(), config.GRPCPort)
	if err != nil {
		return CMUXOut{}, err
	}

	httpListener := cmuxServer.Match(cmux.HTTP1Fast())
	grpcListener := cmuxServer.Match(cmux.Any()) // the rest are GRPC requests

	return CMUXOut{
		CMUX:         cmuxServer,
		GRPCListener: grpcListener,
		HTTPListener: httpListener,
	}, nil
}

func startCMUXServer(logger *zap.Logger, s fx.Shutdowner, input CMUXOut, grpcServer *grpc.Server, httpServer HealthHTTPServer) {
	invokeAsync("cmux server", logger, s, func() error {
		return input.CMUX.Serve()
	})

	invokeAsync("http server", logger, s, func() error {
		return httpServer.Serve(input.HTTPListener)
	})

	invokeAsync("grpc server", logger, s, func() error {
		return grpcServer.Serve(input.GRPCListener)
	})
}

func stopCMUXServer(
	logger *zap.Logger,
	sandboxFactory *sandbox.Factory,
	input CMUXOut,
	grpcServer *grpc.Server,
	httpServer HealthHTTPServer,
	serviceInfo *service.ServiceInfo,
) {
	stopCMUXServerMockable(logger, sandboxFactory, input, grpcServer, httpServer, serviceInfo)
}

func stopCMUXServerMockable(
	logger *zap.Logger,
	sandboxFactory interface{ Wait() },
	input CMUXOut,
	grpcServer *grpc.Server,
	httpServer HealthHTTPServer,
	serviceInfo *service.ServiceInfo,
) {
	// prevent new sandboxes from being created
	if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
		serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_Draining)
	}

	// wait for existing sandboxes to quit
	sandboxFactory.Wait()

	// complete existing requests, prevent new ones from being processed
	grpcServer.GracefulStop()

	// close grpc listener
	if err := input.GRPCListener.Close(); err != nil {
		logger.Error("failed to close grpc listener", zap.Error(err))
	}

	// shutdown the http server
	if err := httpServer.Shutdown(context.Background()); err != nil {
		logger.Error("failed to shutdown cmux server", zap.Error(err))
	}

	// close the http listener
	if err := input.HTTPListener.Close(); err != nil {
		logger.Error("failed to close http listener", zap.Error(err))
	}

	// finally, close the mux
	input.CMUX.Close()
}

func newInfoService(
	sandboxes *sandbox.Map,
	serviceInfo *service.ServiceInfo,
	globalLogger *zap.Logger,
) *service.Server {
	s := service.NewInfoService(serviceInfo, sandboxes)
	globalLogger.Info("Registered gRPC service", zap.String("service", "orchestrator_info.InfoService"))

	return s
}

func newGRPCServer(
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
