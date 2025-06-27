package utils

import (
	"context"
	"encoding/json"
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

func ParseJSONBody[B any](ctx context.Context, body io.ReadCloser) (*B, error) {
	defer body.Close()

	var result B

	err := json.NewDecoder(body).Decode(&result)
	if err != nil {
		return nil, fmt.Errorf("error when parsing request: %w", err)
	}

	return &result, nil
}
