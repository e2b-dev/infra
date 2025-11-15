package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1Info(c *gin.Context) {
	info := a.info

	c.JSON(
		http.StatusOK,
		api.ClusterNodeInfo{
			NodeID:               info.NodeID,
			ServiceInstanceID:    info.ServiceInstanceID,
			ServiceStatus:        info.GetStatus(),
			ServiceStartup:       info.ServiceStartup,
			ServiceVersion:       info.ServiceVersion,
			ServiceVersionCommit: info.ServiceVersionCommit,
		},
	)
}
