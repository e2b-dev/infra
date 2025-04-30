package edge

import (
	"context"
	"go.uber.org/zap"
	"time"
)

// Run todo: this should be ideally done in main file
func Run(ctx context.Context, logger *zap.Logger, proxyDrainingHandler func()) error {
	service, err := NewService(ctx, logger, proxyDrainingHandler)
	if err != nil {
		logger.Error("failed to create service", zap.Error(err))
		return err
	}

	errorChan := make(chan error)

	go func() {
		err := service.Start()
		if err != nil {
			logger.Error("failed to start edge service", zap.Error(err))
			errorChan <- err
		}
	}()

	service.StartServiceDiscovery(ctx)

	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCtxCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCtxCancel()

		logger.Info("context cancelled, shutting down edge service")
		return service.Shutdown(shutdownCtx)
	case err := <-errorChan:
		if err != nil {
			logger.Error("error during service run", zap.Error(err))
			return err
		}
	}

	return nil
}
