package utils

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"

	"github.com/e2b-dev/infra/packages/shared/pkg/telemetry"
)

func ParseBody[B any](ctx context.Context, c *gin.Context) (body B, err error) {
	err = c.Bind(&body)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return body, fmt.Errorf("error when parsing request: %w", err)
	}

	return body, nil
}
