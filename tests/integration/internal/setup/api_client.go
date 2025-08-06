package setup

import (
	"context"
	"fmt"
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

func WithAPIKey(apiKey ...string) func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		apiKey_ := APIKey
		if len(apiKey) > 0 {
			apiKey_ = apiKey[0]
		}
		req.Header.Set("X-API-Key", apiKey_)

		return nil
	}
}

func WithAccessToken() func(ctx context.Context, req *http.Request) error {
	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", AccessToken))

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

func WithSupabaseTeam(t *testing.T, teamID ...string) func(ctx context.Context, req *http.Request) error {
	teamID_ := TeamID
	if len(teamID) > 0 {
		teamID_ = teamID[0]
	}
	if teamID_ == "" {
		t.Skip("Supabase team ID is not set")
	}

	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-Supabase-Team", teamID_)

		return nil
	}
}
