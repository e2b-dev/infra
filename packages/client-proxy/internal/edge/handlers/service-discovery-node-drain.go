package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1ServiceDiscoveryNodeDrain(c *gin.Context, serviceID string) {
	_, templateSpan := a.tracer.Start(c, "service-discovery-node-drain-handler")
	defer templateSpan.End()

	// requests was for this node
	if serviceID == a.info.ServiceInstanceID {
		a.info.SetStatus(api.Draining)
		c.Status(http.StatusOK)
		return
	}

	reqTimeout, reqTimeoutCancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer reqTimeoutCancel()

	// send request to neighboring node
	err := a.sendNodeRequest(reqTimeout, serviceID, api.Draining)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery service")
		return
	}

	c.Status(http.StatusOK)
}

func (a *APIStore) sendNodeRequest(ctx context.Context, serviceID string, status api.ClusterNodeStatus) error {
	findCtx, findCtxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer findCtxCancel()

	logger := a.logger.With(zap.String("service_id", serviceID))

	// try to find orchestrator node first
	o, ok := a.orchestratorPool.GetOrchestrator(serviceID)
	if ok {
		logger.Info("found orchestrator node, calling status change request")

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
			return errors.New("failed to transform node status to orchestrator status")
		}

		_, err := o.GetClient().Info.ServiceStatusOverride(
			findCtx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: orchestratorStatus},
		)
		if err != nil {
			logger.Error("failed to request orchestrator status change", zap.Error(err))
			return errors.New("failed to request orchestrator status change")
		}

		return nil
	}

	// try to find edge node
	e, err := a.edgePool.GetInstanceByID(serviceID)
	if err != nil {
		logger.Error("failed to get node from edge pool", zap.Error(err))
		return errors.New("failed to get edge node")
	}

	switch status {
	case api.Draining:
		_, err = e.GetClient().V1ServiceDiscoveryNodeDrain(ctx, serviceID)
	case api.Unhealthy:
		_, err = e.GetClient().V1ServiceDiscoveryNodeKill(ctx, serviceID)
	default:
		return errors.New("failed to transform node status to api call")
	}

	if err != nil {
		logger.Error("failed to request edge node status change", zap.Error(err))
		return errors.New("failed to request edge node status change")
	}

	return nil
}
