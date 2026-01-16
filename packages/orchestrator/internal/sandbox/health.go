package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"go.uber.org/zap"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
	"github.com/e2b-dev/infra/packages/shared/pkg/logger"
)

func (c *Checks) getHealth(ctx context.Context, timeout time.Duration) (bool, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	address := fmt.Sprintf("http://%s:%d/health", c.sandbox.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return false, err
	}

	response, err := sandboxHttpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer func() {
		// Drain the response body to reuse the connection
		// From response.Body docstring:
		//  // The default HTTP client's Transport may not reuse HTTP/1.x "keep-alive" TCP connections
		//  if the Body is not read to completion and closed.
		if _, err := io.Copy(io.Discard, response.Body); err != nil {
			logger.L().Error(ctx, "failed to drain response body", zap.Error(err), logger.WithSandboxID(c.sandbox.Runtime.SandboxID))
		}
		if err := response.Body.Close(); err != nil {
			logger.L().Error(ctx, "failed to close response body", zap.Error(err), logger.WithSandboxID(c.sandbox.Runtime.SandboxID))
		}
	}()

	if response.StatusCode != http.StatusNoContent {
		return false, fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	return true, nil
}
