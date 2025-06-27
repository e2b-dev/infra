package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/http/edge"
)

// HealthCheck is used by load balancer to check if the Edge API and Edge GRPC proxy services are healthy.
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

// HealthCheckTraffic is used by load balancer target group to check if sandbox traffic should be routed to this instance.
func (a *APIStore) HealthCheckTraffic(c *gin.Context) {
	if a.info.GetStatus() == api.Healthy {
		c.Status(http.StatusOK)
		c.Writer.Write([]byte("healthy"))
		return
	}

	c.Status(http.StatusServiceUnavailable)
	c.Writer.Write([]byte("unhealthy"))
}

// HealthCheckMachine is used mainly for instances management such as autoscaling group to notify instance is ready for safe termination.
func (a *APIStore) HealthCheckMachine(c *gin.Context) {
	if a.info.IsTerminating() {
		c.Status(http.StatusServiceUnavailable)
		c.Writer.Write([]byte("service is terminating"))
		return
	}

	c.Status(http.StatusOK)
	c.Writer.Write([]byte("running"))
}
