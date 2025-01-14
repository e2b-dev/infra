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

		errMsg := fmt.Errorf("error when parsing request: %w", err)
		telemetry.ReportCriticalError(ctx, errMsg)

		return
	}

	node := a.orchestrator.GetNode(nodeId)
	if node == nil {
		c.Status(http.StatusNotFound)
		return
	}

	node.SetStatus(body.Status)

	c.Status(http.StatusNoContent)
}
