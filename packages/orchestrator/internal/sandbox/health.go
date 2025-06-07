package sandbox

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/e2b-dev/infra/packages/shared/pkg/consts"
)

func (s *Sandbox) HealthCheck(ctx context.Context) (bool, error) {
	address := fmt.Sprintf("http://%s:%d/health", s.Slot.HostIPString(), consts.DefaultEnvdServerPort)

	request, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return false, err
	}

	response, err := httpClient.Do(request)
	if err != nil {
		return false, err
	}
	defer func() {
		// Drain the response body to reuse the connection
		// From response.Body docstring:
		//  // The default HTTP client's Transport may not reuse HTTP/1.x "keep-alive" TCP connections
		//  if the Body is not read to completion and closed.
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
	}()

	if response.StatusCode != http.StatusNoContent {
		return false, fmt.Errorf("unexpected status code: %d", response.StatusCode)
	}

	return true, nil
}
