package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

func (a *APIStore) V1ServiceDiscoveryNodeUpdate(c *gin.Context, nodeId string) {
	findCtx, findCtxCancel := context.WithTimeout(c, 5*time.Second)
	defer findCtxCancel()

	logger := a.logger.With(zap.String("node_id", nodeId))

	node, err := a.serviceDiscovery.GetNodeById(findCtx, nodeId)
	if err != nil {
		logger.Error("failed to get node by id", zap.Error(err))

		if errors.Is(err, service_discovery.NodeNotFoundErr) {
			a.sendAPIStoreError(c, http.StatusNotFound, "node not found")
		} else {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to get node by id")
		}

		return
	}

	currentNodeId := a.serviceDiscovery.GetSelfNodeId()
	if nodeId != currentNodeId {
		logger.Info("sending update node request to neighbor", zap.String("node_ip", node.NodeIp))

		err := a.serviceDiscoveryCallNeighborNodes(c, nodeId, node.NodeIp, node.NodePort, "update")
		if err != nil {
			logger.Error("failed to call node update", zap.Error(err))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to call update node")
			return
		}

		logger.Info("cluster node update request delivered")
		c.Status(http.StatusOK)
		return
	}

	// handle self update process
	if a.selfUpdateHandler == nil {
		logger.Error("self update handler is not set")
		a.sendAPIStoreError(c, http.StatusInternalServerError, "self update handler is not configured")
		return
	}

	logger.Info("starting self-update process")

	err = (*a.selfUpdateHandler)()
	if err != nil {
		logger.Error("failed to start self update process", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, err.Error())
		return
	}

	a.healthStatus = api.Draining
	c.Status(http.StatusOK)
}
