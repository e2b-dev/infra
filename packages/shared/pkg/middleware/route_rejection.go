package middleware

import (
	"context"

	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

type RouteTemplate struct {
	Method string
	Path   string
}

type RouteRejection struct {
	Reason  string
	Status  int
	Message string
}

func RejectRoutes(routes []RouteTemplate, shouldReject func(context.Context) bool, rejection RouteRejection) gin.HandlerFunc {
	matchedRoutes := make(map[string]struct{}, len(routes))
	for _, route := range routes {
		matchedRoutes[route.Method+" "+route.Path] = struct{}{}
	}

	return func(c *gin.Context) {
		route := c.FullPath()
		if _, ok := matchedRoutes[c.Request.Method+" "+route]; !ok || !shouldReject(c.Request.Context()) {
			return
		}

		telemetry.ReportEvent(c.Request.Context(), "route rejected",
			attribute.String("http.method", c.Request.Method),
			attribute.String("http.route", route),
			attribute.String("route.rejection.reason", rejection.Reason),
			attribute.Int("http.response.status_code", rejection.Status),
		)
		c.AbortWithStatusJSON(rejection.Status, gin.H{
			"code":    rejection.Status,
			"message": rejection.Message,
		})
	}
}
