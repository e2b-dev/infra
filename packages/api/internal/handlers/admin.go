package handlers

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/orchestrator"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
)

func (a *APIStore) GetNodes(c *gin.Context) {
	ctx := c.Request.Context()
	result := a.orchestrator.AdminNodes(ctx)
	c.JSON(http.StatusOK, result)
}

func (a *APIStore) GetNodesNodeID(c *gin.Context, nodeID api.NodeID, params api.GetNodesNodeIDParams) {
	ctx := c.Request.Context()

	clusterID := utils.WithClusterFallback(params.ClusterID)
	result, err := a.orchestrator.AdminNodeDetail(clusterID, nodeID)
	if err != nil {
		if errors.Is(err, orchestrator.ErrNodeNotFound) {
			c.Status(http.StatusNotFound)

			return
		}

		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, "Error when getting node details", err)

		return
	}

	c.JSON(http.StatusOK, result)
}

func (a *APIStore) PostNodesNodeID(c *gin.Context, nodeId api.NodeID) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.PostNodesNodeIDJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err), err)

		return
	}

	clusterID := utils.WithClusterFallback(body.ClusterID)
	node := a.orchestrator.GetNodeByIDOrNomadShortID(clusterID, nodeId)
	if node == nil {
		c.Status(http.StatusNotFound)

		return
	}

	err = node.SendStatusChange(ctx, body.Status)
	if err != nil {
		a.sendAPIStoreError(ctx, c, http.StatusInternalServerError, fmt.Sprintf("Error when sending status change: %s", err), err)

		return
	}

	c.Status(http.StatusNoContent)
}
