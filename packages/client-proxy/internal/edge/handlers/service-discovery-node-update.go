package handlers

import (
	"context"
	"errors"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/edge/service-discovery"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"net/http"
	"time"
)

func (a *APIStore) PostV1ServiceDiscoveryNodesNodeIdUpdate(c *gin.Context, nodeId string) {
	findCtx, findCtxCancel := context.WithTimeout(c, 5*time.Second)
	defer findCtxCancel()

	logger := a.logger.With(zap.String("node_id", nodeId))

	node, err := a.serviceDiscovery.GetNodeById(findCtx, nodeId)
	if err != nil {
		logger.Error("failed to get node by id", zap.Error(err))

		if errors.Is(err, service_discovery.ServiceNotFoundErr) {
			a.sendAPIStoreError(c, http.StatusNotFound, "node not found")
		} else {
			a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to get node by id")
		}

		return
	}

	currentNodeId := a.serviceDiscovery.GetItself()
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

	resp := (*a.selfUpdateHandler)()
	if !resp.Success {
		logger.Error("failed to start self update process", zap.Error(resp.Error))
		a.sendAPIStoreError(c, http.StatusInternalServerError, resp.Message)
		return
	}

	a.healthStatus = api.Draining
	c.Status(http.StatusOK)
}
