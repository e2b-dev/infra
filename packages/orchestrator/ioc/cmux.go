package ioc

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/soheilhy/cmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
	"google.golang.org/grpc"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/factories"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/service"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
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

func withCMUXWaitBeforeShutdown(f any) any {
	return fx.Annotate(
		f, fx.ParamTags(cmuxWaitBeforeShutdownGroupTag),
	)
}

func newCMUXModule() fx.Option {
	return fx.Module(
		"cmux",
		fx.Invoke(
			withCMUXWaitBeforeShutdown(startCMUXServer),
		),
	)
}

func startCMUXServer(
	waitBeforeShutdown []CMUXWaitBeforeShutdown,
	config cfg.Config,
	logger logger.Logger,
	s fx.Shutdowner,
	grpcServer *grpc.Server,
	httpServer HealthHTTPServer,
	lc fx.Lifecycle,
	serviceInfo *service.ServiceInfo,
) error {
	// cmux server, allows us to reuse the same TCP port between grpc and HTTP requests
	cmuxServer, err := factories.NewCMUXServer(context.Background(), config.GRPCPort)
	if err != nil {
		return fmt.Errorf("failed to create cmux server: %w", err)
	}

	httpListener := cmuxServer.Match(cmux.HTTP1Fast())
	grpcListener := cmuxServer.Match(cmux.Any()) // the rest are GRPC requests

	invokeAsync(s, func() error {
		if err := cmuxServer.Serve(); ignoreUseOfClosed(err) != nil {
			return err
		}

		return nil
	})

	invokeAsync(s, func() error {
		if err := httpServer.Serve(httpListener); ignoreUseOfClosed(err) != nil {
			return err
		}

		return nil
	})

	invokeAsync(s, func() error {
		if err := grpcServer.Serve(grpcListener); ignoreUseOfClosed(err) != nil {
			return err
		}

		return nil
	})

	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			stopCMUXServerMockable(
				ctx, logger,
				grpcListener, grpcServer,
				httpListener, httpServer,
				cmuxServer, serviceInfo,
				waitBeforeShutdown,
			)

			return nil
		},
	})

	return nil
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

const (
	preShutdownTimeout  = 5 * time.Second
	httpShutdownTimeout = 15 * time.Second
)

func stopCMUXServerMockable(
	ctx context.Context,
	logger logger.Logger,
	grpcListener net.Listener,
	grpcServer *grpc.Server,
	httpListener net.Listener,
	httpServer HealthHTTPServer,
	cmuxServer cmux.CMux,
	serviceInfo *service.ServiceInfo,
	preCMUXShutdowns []CMUXWaitBeforeShutdown,
) {
	logger.Info(ctx, "marking service as 'draining'")
	if serviceInfo.GetStatus() == orchestratorinfo.ServiceInfoStatus_Healthy {
		serviceInfo.SetStatus(ctx, orchestratorinfo.ServiceInfoStatus_Draining)
	}

	for _, preShutdown := range preCMUXShutdowns {
		preCtx, cancel := context.WithTimeout(ctx, preShutdownTimeout)
		if err := preShutdown.Wait(preCtx); err != nil {
			logger.Warn(ctx, "failed to wait for pre-shutdown hook",
				zap.Error(err),
			)
		}
		cancel()
	}

	logger.Info(ctx, "gracefully shutting down grpc server")
	grpcServer.GracefulStop()

	logger.Info(ctx, "closing grpc listener")
	if err := grpcListener.Close(); ignoreUseOfClosed(err) != nil {
		logger.Error(ctx, "failed to close grpc listener", zap.Error(err))
	}

	ctx, cancel := context.WithTimeout(ctx, httpShutdownTimeout)
	defer cancel()

	logger.Info(ctx, "gracefully shutting down http server")
	if err := httpServer.Shutdown(ctx); ignoreUseOfClosed(err) != nil {
		logger.Error(ctx, "failed to shutdown cmux server", zap.Error(err))
	}

	logger.Info(ctx, "closing http listener")
	if err := httpListener.Close(); ignoreUseOfClosed(err) != nil {
		logger.Error(ctx, "failed to close http listener", zap.Error(err))
	}

	logger.Info(ctx, "closing the cmux server")
	cmuxServer.Close()
}
