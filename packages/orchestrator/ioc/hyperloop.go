package ioc

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/orchestrator/internal/cfg"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/hyperloopserver"
	"github.com/e2b-dev/infra/packages/orchestrator/internal/sandbox"
)

func NewHyperloopModule() fx.Option {
	return fx.Module("hyperloop",
		fx.Provide(
			newHyperloopServer,
		),
		fx.Invoke(
			startHyperloopServer,
		),
	)
}

// HyperloopHTTPServer wraps the hyperloop HTTP server to distinguish it from HealthHTTPServer in DI
type HyperloopHTTPServer struct {
	*http.Server
}

func newHyperloopServer(
	lc fx.Lifecycle,
	config cfg.Config,
	logger *zap.Logger,
	sandboxes *sandbox.Map,
) (HyperloopHTTPServer, error) {
	hyperloopSrv, err := hyperloopserver.NewHyperloopServer(config.NetworkConfig.HyperloopProxyPort, logger, sandboxes)
	if err != nil {
		return HyperloopHTTPServer{}, fmt.Errorf("failed to create hyperloop server: %w", err)
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			go func() {
				err := hyperloopSrv.ListenAndServe()
				if err != nil && !errors.Is(err, http.ErrServerClosed) {
					logger.Error("Hyperloop server error", zap.Error(err))
				}
			}()

			return nil
		},
		OnStop: func(ctx context.Context) error {
			return hyperloopSrv.Shutdown(ctx)
		},
	})

	return HyperloopHTTPServer{hyperloopSrv}, nil
}

func startHyperloopServer(HyperloopHTTPServer) {}
