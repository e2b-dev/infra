package ginutils

import (
	"context"
	"fmt"
	"io"

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

func ParseBodyWith[B any](ctx context.Context, c *gin.Context, parse func(io.Reader) (B, error)) (body B, err error) {
	body, err = parse(c.Request.Body)
	if err != nil {
		telemetry.ReportCriticalError(ctx, "error when parsing request", err)

		return body, fmt.Errorf("error when parsing request: %w", err)
	}

	return body, nil
}
