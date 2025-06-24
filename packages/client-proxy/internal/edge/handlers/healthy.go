package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

func (a *APIStore) HealthCheck(c *gin.Context) {
	status := a.info.GetStatus()

	if status == api.Healthy || status == api.Draining {
		c.Status(http.StatusOK)
		c.Writer.Write([]byte("healthy"))
		return
	}

	c.Status(http.StatusServiceUnavailable)
	c.Writer.Write([]byte("unhealthy"))
}

func (a *APIStore) HealthCheckTraffic(c *gin.Context) {
	if a.info.GetStatus() == api.Healthy {
		c.Status(http.StatusOK)
		c.Writer.Write([]byte("healthy"))
		return
	}

	c.Status(http.StatusServiceUnavailable)
	c.Writer.Write([]byte("unhealthy"))
}

func (a *APIStore) HealthCheckMachine(c *gin.Context) {
	if a.info.GetTerminating() {
		c.Status(http.StatusServiceUnavailable)
		c.Writer.Write([]byte("service is terminating"))
		return
	}

	c.Status(http.StatusOK)
	c.Writer.Write([]byte("running"))
}
