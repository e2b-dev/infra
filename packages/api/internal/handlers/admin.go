package handlers

import (
	"cmp"
	"fmt"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/api/internal/api"
	"github.com/e2b-dev/infra/packages/api/internal/utils"
	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func (a *APIStore) GetNodes(c *gin.Context) {
	nodes := a.orchestrator.GetNodes()

	slices.SortFunc(nodes, func(i, j *api.Node) int {
		return cmp.Compare(i.NodeID, j.NodeID)
	})

	c.JSON(http.StatusOK, nodes)
}

func (a *APIStore) GetNodesNodeID(c *gin.Context, nodeId api.NodeID) {
	node := a.orchestrator.GetNodeDetail(nodeId)

	if node == nil {
		c.Status(http.StatusNotFound)
		return
	}

	c.JSON(http.StatusOK, node)
}

func (a *APIStore) PostNodesNodeID(c *gin.Context, nodeId api.NodeID) {
	ctx := c.Request.Context()

	body, err := utils.ParseBody[api.PostNodesNodeIDJSONRequestBody](ctx, c)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusBadRequest, fmt.Sprintf("Error when parsing request: %s", err))

		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return
	}

	node := a.orchestrator.GetNode(nodeId)
	if node == nil {
		c.Status(http.StatusNotFound)
		return
	}

	err = node.SendStatusChange(ctx, body.Status)
	if err != nil {
		a.sendAPIStoreError(c, http.StatusInternalServerError, fmt.Sprintf("Error when sending status change: %s", err))

		errMsg := fmt.Errorf("error when sending status change: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)
		return
	}

	c.Status(http.StatusNoContent)
}
