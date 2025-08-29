package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetNodes(c *gin.Context) {
	result := a.orchestrator.AdminNodes()
	c.JSON(http.StatusOK, result)
}

func (a *APIStore) GetNodesNodeID(c *gin.Context, nodeID api.NodeID, params api.GetNodesNodeIDParams) {
	clusterID := uuid.Nil
	if params.ClusterID != nil {
		clusterUUID, err := uuid.Parse(*params.ClusterID)
		if err != nil {
			telemetry.ReportCriticalError(c.Request.Context(), "invalid cluster_id", err)
			a.sendAPIStoreError(c, http.StatusBadRequest, "Invalid cluster_id")

			return
		}
		clusterID = clusterUUID
	}

	result, err := a.orchestrator.AdminNodeDetail(clusterID, nodeID)
	if err != nil {
		if errors.Is(err, orchestrator.ErrNodeNotFound) {
			c.Status(http.StatusNotFound)
			return
		}

		telemetry.ReportCriticalError(c.Request.Context(), "error when getting node details", err)
		a.sendAPIStoreError(c, http.StatusInternalServerError, "Error when getting node details")
		return
	}

	c.JSON(http.StatusOK, result)
}

func (a *APIStore) PostNodesNodeID(c *gin.Context, nodeId api.NodeID) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.PostNodesNodeIDJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	node := a.orchestrator.GetNodeByNomadShortID(nodeId)
	if node == nil {
		c.Status(http.StatusNotFound)
		return
	}

	err = node.SendStatusChange(ctx, body.Status)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when sending status change: %s", err))

		telemetry.ReportCriticalError(ctx, "error when sending status change", err)
		return
	}

	c.Status(http.StatusNoContent)
}
