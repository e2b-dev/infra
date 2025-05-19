package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/pool"
	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
)

func (a *APIStore) V1ServiceDiscoveryNodeDrain(c *gin.Context, nodeId string) {
	err := a.sendNodeRequest(c, nodeId, orchestratorinfo.ServiceInfoStatus_OrchestratorDraining)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery node")
		return
	}

	c.Status(http.StatusOK)
}

func (a *APIStore) sendNodeRequest(ctx context.Context, nodeId string, status orchestratorinfo.ServiceInfoStatus) error {
	findCtx, findCtxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer findCtxCancel()

	logger := a.logger.With(zap.String("node_id", nodeId))

	// try to find orchestrator node first
	o, err := a.orchestratorPool.GetOrchestrator(nodeId)
	if err != nil {
		if !errors.Is(err, pool.ErrOrchestratorNotFound) {
			logger.Warn("Failed to get orchestrator", zap.Error(err))
			return errors.New("Error when getting orchestrator node")
		}
	}

	if o != nil {
		logger.Info("found orchestrator node, calling drain request")
		_, err = o.Client.Info.ServiceStatusOverride(
			findCtx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: status},
		)

		if err != nil {
			logger.Error("failed to drain orchestrator node", zap.Error(err))
			return errors.New("Failed to drain orchestrator node")
		}

		return nil
	}

	// todo: call edge api node
	return errors.New("Failed to find node, it must be edge api node")
}
