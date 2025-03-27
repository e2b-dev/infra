package setup

import (
	"context"
	"net/http"
	"testing"

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

func WithSupabaseToken(t *testing.T) func(ctx context.Context, req *http.Request) error {
	if SupabaseToken == "" {
		t.Skip("Supabase token is not set")
	}

	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-Supabase-Token", SupabaseToken)

		return nil
	}
}

func WithSupabaseTeam(t *testing.T) func(ctx context.Context, req *http.Request) error {
	if SupabaseTeamID == "" {
		t.Skip("Supabase team ID is not set")
	}

	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-Supabase-Team", SupabaseTeamID)

		return nil
	}
}
