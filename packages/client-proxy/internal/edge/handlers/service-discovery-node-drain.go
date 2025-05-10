package handlers

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	"github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

func (a *APIStore) V1ServiceDiscoveryNodeDrain(c *gin.Context, nodeId string) {
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
		logger.Info("sending draining node request to neighbor", zap.String("node_ip", node.NodeIp))

		err := a.serviceDiscoveryCallNeighborNodes(c, nodeId, node.NodeIp, node.NodePort, "drain")
		if err != nil {
			logger.Error("failed to call node drain", zap.Error(err))
			a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to call neighbor node")
			return
		}

		logger.Info("cluster node drain request delivered")
		c.Status(http.StatusOK)
		return
	}

	// handle self drain process
	if a.selfDrainHandler == nil {
		logger.Error("self drain handler is not set")
		a.sendAPIStoreError(c, http.StatusInternalServerError, "self drain handler is not configured")
		return
	}

	logger.Info("starting self-drain process")

	err = (*a.selfDrainHandler)()
	if err != nil {
		logger.Error("failed to start self drain process", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to start self drain process")
		return
	}

	a.healthStatus = api.Draining
	c.Status(http.StatusOK)
}

func (a *APIStore) serviceDiscoveryCallNeighborNodes(ctx context.Context, nodeId string, nodeIp string, nodePort int, callMethod string) error {
	// todo: add authorization when implemented
	// update / drain
	reqUrl := fmt.Sprintf("http://%s:%d/v1/service-discovery/nodes/%s/%s", nodeIp, nodePort, nodeId, callMethod)
	req, err := http.NewRequest(http.MethodPost, reqUrl, nil)
	if err != nil {
		return err
	}

	reqCtx, reqCtxCancel := context.WithTimeout(ctx, 5*time.Second)
	defer reqCtxCancel()

	req.WithContext(reqCtx)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}

	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to call neighbor node: %s", res.Status)
	}

	return nil
}
