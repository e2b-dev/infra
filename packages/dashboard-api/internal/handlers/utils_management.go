package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/dashboard-api/internal/api"
)

// sendNotImplemented answers the management operations that are declared in
// the contract but not yet served. Each one is a distinct piece of work, so
// they land individually and this loses a caller each time.
func sendNotImplemented(c *gin.Context) {
	c.JSON(http.StatusNotImplemented, api.Error{
		Code:    http.StatusNotImplemented,
		Message: "operation is not implemented",
	})
}
