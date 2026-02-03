package handlers

import (
	"context"
	"errors"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
	e2bcatalog "github.com/e2b-dev/infra/packages/shared/pkg/sandbox-catalog"
)

func (a *APIStore) deletePausedCatalogEntry(ctx context.Context, sandboxID string) {
	if a.orchestrator == nil {
		return
	}

	if err := a.orchestrator.DeletePausedSandbox(ctx, sandboxID); err != nil && !errors.Is(err, e2bcatalog.ErrPausedSandboxNotFound) {
		logger.L().Warn(ctx, "error removing paused sandbox record", zap.Error(err), logger.WithSandboxID(sandboxID))
	}
}
