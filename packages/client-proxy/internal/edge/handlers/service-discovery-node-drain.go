package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	orchestratorinfo "github.com/e2b-dev/infra/packages/shared/pkg/grpc/orchestrator-info"
	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) V1ServiceDiscoveryNodeDrain(c *gin.Context) {
	ctx := c.Request.Context()

	spanCtx, templateSpan := a.tracer.Start(ctx, "service-discovery-node-drain-handler")
	defer templateSpan.End()

	body, err := parseBody[api.V1ServiceDiscoveryNodeDrainJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)
		return
	}

	// requests was for this service instance
	if body.ServiceInstanceID == a.info.ServiceInstanceID && body.ServiceType == api.ClusterNodeTypeEdge {
		a.info.SetStatus(api.Draining)
		c.Status(http.StatusOK)
		return
	}

	reqTimeout, reqTimeoutCancel := context.WithTimeout(spanCtx, 5*time.Second)
	defer reqTimeoutCancel()

	// send request to neighboring node
	err = a.sendNodeRequest(reqTimeout, body.ServiceInstanceID, body.ServiceType, api.Draining)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, "Error when calling service discovery service")
		return
	}

	c.Status(http.StatusOK)
}

func (a *APIStore) sendNodeRequest(ctx context.Context, serviceInstanceID string, serviceType api.ClusterNodeType, status api.ClusterNodeStatus) error {
	switch serviceType {
	case api.ClusterNodeTypeEdge:
		return a.sendEdgeRequest(ctx, serviceInstanceID, status)
	case api.ClusterNodeTypeOrchestrator:
		return a.sendOrchestratorRequest(ctx, serviceInstanceID, status)
	}

	return errors.New("invalid service type")
}

func (a *APIStore) sendOrchestratorRequest(ctx context.Context, serviceInstanceID string, status api.ClusterNodeStatus) error {
	logger := a.logger.With(l.WithServiceInstanceID(serviceInstanceID))

	// try to find orchestrator node first
	o, ok := a.orchestratorPool.GetOrchestrator(serviceInstanceID)
	if !ok {
		return errors.New("orchestrator instance doesn't found")
	}

	logger.Info("orchestrator instance found, calling status change request")
	var orchestratorStatus orchestratorinfo.ServiceInfoStatus

	switch status {
	case api.Draining:
		orchestratorStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorDraining
	case api.Unhealthy:
		orchestratorStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorUnhealthy
	case api.Healthy:
		orchestratorStatus = orchestratorinfo.ServiceInfoStatus_OrchestratorHealthy
	default:
		logger.Error("failed to transform service status to orchestrator status", zap.String("status", string(status)))
		return errors.New("failed to transform service status to orchestrator status")
	}

	findCtx, findCtxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer findCtxCancel()

	_, err := o.GetClient().Info.ServiceStatusOverride(
		findCtx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: orchestratorStatus},
	)
	if err != nil {
		logger.Error("failed to request orchestrator status change", zap.Error(err))
		return errors.New("failed to request orchestrator status change")
	}

	return nil
}

func (a *APIStore) sendEdgeRequest(ctx context.Context, serviceInstanceID string, status api.ClusterNodeStatus) error {
	logger := a.logger.With(l.WithServiceInstanceID(serviceInstanceID))

	// try to find edge node
	e, err := a.edgePool.GetInstanceByID(serviceInstanceID)
	if err != nil {
		logger.Error("failed to get service instance from edge pool", zap.Error(err))
		return errors.New("failed to get edge service instance")
	}

	switch status {
	case api.Draining:
		req := api.V1ServiceDiscoveryNodeDrainJSONRequestBody{ServiceType: api.ClusterNodeTypeEdge, ServiceInstanceID: serviceInstanceID}
		_, err = e.GetClient().V1ServiceDiscoveryNodeDrain(ctx, req)
	case api.Unhealthy:
		req := api.V1ServiceDiscoveryNodeKillJSONRequestBody{ServiceType: api.ClusterNodeTypeEdge, ServiceInstanceID: serviceInstanceID}
		_, err = e.GetClient().V1ServiceDiscoveryNodeKill(ctx, req)
	default:
		return errors.New("failed to transform service instance status to api call")
	}

	if err != nil {
		logger.Error("failed to request edge service instance status change", zap.Error(err))
		return errors.New("failed to request edge service instance status change")
	}

	return nil
}
