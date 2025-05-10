package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/proxy/internal/edge/api"
)

func (a *APIStore) GetHealth(c *gin.Context) {
	if a.healthStatus == api.Healthy || a.healthStatus == api.Draining {
		c.Status(http.StatusOK)
		c.Writer.Write([]byte("healthy"))
		return
	}

	c.Status(http.StatusServiceUnavailable)
	c.Writer.Write([]byte("unhealthy"))
}

func (a *APIStore) GetHealthTraffic(c *gin.Context) {
	if a.healthStatus == api.Healthy {
		c.Status(http.StatusOK)
		c.Writer.Write([]byte("healthy"))
		return
	}

	c.Status(http.StatusServiceUnavailable)
	c.Writer.Write([]byte("unhealthy"))
}
