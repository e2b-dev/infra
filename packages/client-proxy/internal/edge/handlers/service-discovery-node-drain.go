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
	api "github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
	l "github.com/e2b-dev/infra/packages/shared/pkg/logger"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

var ApiNodeToOrchestratorStateMapper = map[api.ClusterNodeStatus]orchestratorinfo.ServiceInfoStatus{
	api.Healthy:   orchestratorinfo.ServiceInfoStatus_Healthy,
	api.Draining:  orchestratorinfo.ServiceInfoStatus_Draining,
	api.Unhealthy: orchestratorinfo.ServiceInfoStatus_Unhealthy,
}

func (a *APIStore) V1ServiceDiscoveryNodeDrain(c *gin.Context) {
	ctx := c.Request.Context()

	spanCtx, templateSpan := tracer.Start(ctx, "service-discovery-node-drain-handler")
	defer templateSpan.End()

	body, err := parseBody[api.V1ServiceDiscoveryNodeDrainJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

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

	logger.Info(ctx, "orchestrator instance found, calling status change request")

	findCtx, findCtxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer findCtxCancel()

	orchestratorStatus := ApiNodeToOrchestratorStateMapper[status]
	_, err := o.GetClient().Info.ServiceStatusOverride(
		findCtx, &orchestratorinfo.ServiceStatusChangeRequest{ServiceStatus: orchestratorStatus},
	)
	if err != nil {
		logger.Error(ctx, "failed to request orchestrator status change", zap.Error(err))

		return errors.New("failed to request orchestrator status change")
	}

	return nil
}
