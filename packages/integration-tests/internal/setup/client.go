package setup

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/integration-tests/internal/api"
)

func GetAPIClient(tb testing.TB) *api.ClientWithResponses {
	hc := http.Client{
		Timeout: apiTimeout,
	}

	c, err := api.NewClientWithResponses(APIServerURL, api.WithHTTPClient(&hc))
	if err != nil {
		tb.Fatal(err)

		return nil
	}

	return c
}

func WithAPIKey() func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-API-Key", APIKey)

		return nil
	}
}
