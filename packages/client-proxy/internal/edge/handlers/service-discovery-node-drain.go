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
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryNodeDrain(c *gin.Context, serviceId string) {
	// requests was for this node
	if serviceId == a.info.ServiceId {
		a.info.SetStatus(api.Draining)
		c.Status(http.StatusOK)
		return
	}

	reqTimeout, reqTimeoutCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer reqTimeoutCancel()

	// send request to neighboring node
	err := a.sendNodeRequest(reqTimeout, serviceId, api.Draining)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery service")
		return
	}

	c.Status(http.StatusOK)
}

func (a *APIStore) sendNodeRequest(ctx context.Context, serviceId string, status api.ClusterNodeStatus) error {
	findCtx, findCtxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer findCtxCancel()

	logger := a.logger.With(zap.String("service_id", serviceId))

	// try to find orchestrator node first
	o, err := a.orchestratorPool.GetOrchestrator(serviceId)
	if err != nil {
		if !errors.Is(err, pool.ErrOrchestratorNotFound) {
			logger.Warn("Failed to get orchestrator", zap.Error(err))
			return errors.New("Error when getting orchestrator node")
		}
	}

	if o != nil {
		logger.Info("found orchestrator node, calling drain request")

		var orchestratorStatus orchestratorinfo.ServiceInfoStatus

		switch status {
		case api.Draining:
			orchestratorStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorDraining
		case api.Unhealthy:
			orchestratorStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorUnhealthy
		case api.Healthy:
			orchestratorStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy
		default:
			logger.Error("failed to transform node status to orchestrator status", zap.String("status", string(status)))
			return errors.New("Failed to transform node status to orchestrator status")
		}

		_, err = o.Client.Info.ServiceStatusOverride(
			findCtx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: orchestratorStatus},
		)

		if err != nil {
			logger.Error("failed to request orchestrator status change", zap.Error(err))
			return errors.New("Failed to request orchestrator status change")
		}

		return nil
	}

	// try to find edge node
	e, err := a.edgePool.GetNode(serviceId)
	if err != nil {
		logger.Error("failed to get edge node", zap.Error(err))
		return errors.New("Failed to get edge node")
	}

	switch status {
	case api.Draining:
		_, err = e.Client.V1ServiceDiscoveryNodeDrain(ctx, serviceId)
	case api.Unhealthy:
		_, err = e.Client.V1ServiceDiscoveryNodeKill(ctx, serviceId)
	default:
		return errors.New("Failed to transform node status to api call")
	}

	if err != nil {
		logger.Error("failed to request edge node status change", zap.Error(err))
		return errors.New("Failed to request edge node status change")
	}

	return nil
}
