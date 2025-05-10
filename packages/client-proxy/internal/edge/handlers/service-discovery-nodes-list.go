package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/service-discovery"
)

func (a *APIStore) V1ServiceDiscoveryNodes(c *gin.Context) {
	nodes, err := a.serviceDiscovery.ListNodes(c)
	if err != nil {
		a.logger.Error("failed to list cluster nodes", zap.Error(err))
		a.sendAPIStoreError(c, http.StatusInternalServerError, "failed to list cluster nodes")
		return
	}

	nodesRes := make([]api.ClusterNode, 0, len(nodes))

	for nodeId, node := range nodes {
		nodeStatus, err := getNodeStatusResolved(node.Status)
		if err != nil {
			a.logger.Error("failed to resolve node status", zap.String("node_id", nodeId), zap.Error(err))
			continue
		}

		nodeType, err := getNodeTypeResolved(node.ServiceType)
		if err != nil {
			a.logger.Error("failed to resolve node type", zap.String("node_id", nodeId), zap.Error(err))
			continue
		}

		nodesRes = append(
			nodesRes,
			api.ClusterNode{
				Id:           nodeId,
				Status:       nodeStatus,
				Type:         nodeType,
				Version:      node.ServiceVersion,
				NodeIp:       node.NodeIp,
				NodePort:     node.NodePort,
				RegisteredAt: time.Unix(node.RegisteredAt, 0),
				ExpiresAt:    time.Unix(node.ExpiresAt, 0),
			},
		)
	}

	c.JSON(http.StatusOK, nodesRes)
}

func getNodeStatusResolved(s string) (api.ClusterNodeStatus, error) {
	switch s {
	case "healthy":
		return api.Healthy, nil
	case "draining":
		return api.Draining, nil
	case "unhealthy":
		return api.Unhealthy, nil
	default:
		return "", fmt.Errorf("unknown node status: %s", s)
	}
}

func getNodeTypeResolved(t string) (api.ClusterNodeType, error) {
	switch t {
	case service_discovery.ServiceTypeEdge:
		return api.Edge, nil
	case service_discovery.ServiceTypeOrchestrator:
		return api.Orchestrator, nil
	default:
		return "", fmt.Errorf("unknown node type: %s", t)
	}
}
