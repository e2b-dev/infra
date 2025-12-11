package ioc

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/fx"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

type HyperloopServer struct {
	*http.Server
}

func startHyperloopServer(lc fx.Lifecycle, server HyperloopServer, s fx.Shutdowner, logger logger.Logger) {
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			logger.Info(ctx, "shutting down hyperloop server")
			defer logger.Info(ctx, "hyperloop server shutdown complete")

			return server.Shutdown(ctx)
		},
	})

	invokeAsync(s, func() error {
		err := server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}

		return err
	})
}
