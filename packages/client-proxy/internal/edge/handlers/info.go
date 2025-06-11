package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) V1Info(c *gin.Context) {
	info := a.info

	c.Status(http.StatusOK)
	c.JSON(
		http.StatusOK,
		api.ClusterNodeInfo{
			Id:      info.ServiceId,
			NodeId:  info.NodeId,
			Status:  info.GetStatus(),
			Startup: info.Startup,
			Version: info.SourceVersion,
			Commit:  info.SourceCommit,
		},
	)
}
