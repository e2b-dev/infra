package setup

import (
	"context"
	"net/http"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
)

func GetAPIClient() *api.ClientWithResponses {
	hc := http.Client{
		Timeout: apiTimeout,
	}

	c, err := api.NewClientWithResponses(APIServerURL, api.WithHTTPClient(&hc))
	if err != nil {
		panic(err)
	}

	return c
}

func WithAPIKey() func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-API-Key", APIKey)

		return nil
	}
}

func WithAccessToken() func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Authorization", "Bearer "+AccessToken)

		return nil
	}
}
