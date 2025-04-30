package handlers

import (
	"fmt"
	service_discovery "github.com/e2b-dev/infra/packages/proxy/internal/edge/service-discovery"
	"github.com/e2b-dev/infra/packages/proxy/internal/edge/updater"
	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
)

type APIStore struct {
	healthStatus api.ClusterNodeStatus

	selfUpdateHandler *func() updater.UpdaterResponse
	selfDrainHandler  *func() error

	logger           *zap.Logger
	serviceDiscovery *service_discovery.ServiceDiscovery
}

func NewStore(serviceDiscovery *service_discovery.ServiceDiscovery, logger *zap.Logger, selfUpdateHandler *func() updater.UpdaterResponse, selfDrainHandler *func() error) (*APIStore, error) {
	return &APIStore{
		serviceDiscovery: serviceDiscovery,
		logger:           logger,
		healthStatus:     api.Healthy,

		selfDrainHandler:  selfDrainHandler,
		selfUpdateHandler: selfUpdateHandler,
	}, nil
}

func (a *APIStore) SetHealth(health api.ClusterNodeStatus) {
	a.healthStatus = health
}

func (a *APIStore) sendAPIStoreError(c *gin.Context, code int, message string) {
	apiErr := api.Error{
		Code:    int32(code),
		Message: message,
	}

	c.Error(fmt.Errorf(message))
	c.JSON(code, apiErr)
}
