package ioc

import (
	"context"
	"net"
	"strings"

	"github.com/soheilhy/cmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

const cmuxWaitBeforeShutdownGroupTag = `group:"cmux-waitable"`

type CMUXWaitBeforeShutdown interface {
	Wait(ctx context.Context) error
}

type cmuxWaitBeforeShutdown struct {
	fn func(context.Context) error
}

func (c cmuxWaitBeforeShutdown) Wait(ctx context.Context) error {
	return c.fn(ctx)
}

var _ CMUXWaitBeforeShutdown = cmuxWaitBeforeShutdown{}

func asCMUXWaitBeforeShutdown(f any) any {
	return fx.Annotate(
		f,
		fx.As((*CMUXWaitBeforeShutdown)(nil)),
		fx.ResultTags(cmuxWaitBeforeShutdownGroupTag),
	)
}

func withCMUXWaitBeforeShutdown(f any) any {
	return fx.Annotate(
		f, fx.ParamTags(cmuxWaitBeforeShutdownGroupTag),
	)
}

func newCMUXModule() fx.Option {
	return fx.Module(
		"cmux",
		fx.Provide(
			newCMUXServer,
		),
		fx.Invoke(
			withCMUXWaitBeforeShutdown(startCMUXServer),
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

func startCMUXServer(
	waitBeforeShutdown []CMUXWaitBeforeShutdown,
	logger *zap.Logger,
	s fx.Shutdowner,
	input CMUXOut,
	grpcServer *grpc.Server,
	httpServer HealthHTTPServer,
	lc fx.Lifecycle,
	sandboxFactory *sandbox.Factory,
	serviceInfo *service.ServiceInfo,
) {
	invokeAsync(s, func() error {
		if err := input.CMUX.Serve(); ignoreUseOfClosed(err) != nil {
			return err
		}

		return nil
	})

	invokeAsync(s, func() error {
		if err := httpServer.Serve(input.HTTPListener); ignoreUseOfClosed(err) != nil {
			return err
		}

		return nil
	})

	invokeAsync(s, func() error {
		if err := grpcServer.Serve(input.GRPCListener); ignoreUseOfClosed(err) != nil {
			return err
		}

		return nil
	})

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			stopCMUXServerMockable(ctx, logger, input, grpcServer, httpServer, serviceInfo, waitBeforeShutdown)
			return nil
		},
	})
}

func ignoreUseOfClosed(err error) error {
	if err == nil {
		return nil
	}

	// pulled from cmux examples. sad.
	if strings.Contains(err.Error(), "use of closed network connection") {
		return nil
	}

	return err
}

func stopCMUXServerMockable(
	ctx context.Context,
	logger *zap.Logger,
	input CMUXOut,
	grpcServer *grpc.Server,
	httpServer HealthHTTPServer,
	serviceInfo *service.ServiceInfo,
	preCMUXShutdowns []CMUXWaitBeforeShutdown,
) {
	logger.Info("marking service as 'draining'")
	if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
		serviceInfo.SetStatus(orchestratorinfo.ServiceInfoStatus_Draining)
	}

	for _, preShutdown := range preCMUXShutdowns {
		if err := preShutdown.Wait(ctx); err != nil {
			logger.Warn("failed to wait for pre-shutdown hook",
				zap.Error(err),
			)
		}
	}

	logger.Info("gracefully shutting down grpc server")
	grpcServer.GracefulStop()

	logger.Info("closing grpc listener")
	if err := input.GRPCListener.Close(); ignoreUseOfClosed(err) != nil {
		logger.Error("failed to close grpc listener", zap.Error(err))
	}

	logger.Info("gracefully shutting down http server")
	if err := httpServer.Shutdown(context.Background()); ignoreUseOfClosed(err) != nil {
		logger.Error("failed to shutdown cmux server", zap.Error(err))
	}

	logger.Info("closing http listener")
	if err := input.HTTPListener.Close(); ignoreUseOfClosed(err) != nil {
		logger.Error("failed to close http listener", zap.Error(err))
	}

	logger.Info("closing the cmux server")
	input.CMUX.Close()
}
