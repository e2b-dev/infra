package setup

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/packages/shared/pkg/tests"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
)

type addHeaders struct {
	headers map[string]string
	rt      http.RoundTripper
}

func (a addHeaders) RoundTrip(request *http.Request) (*http.Response, error) {
	for key, val := range a.headers {
		request.Header.Add(key, val)
	}

	return a.rt.RoundTrip(request)
}

var _ http.RoundTripper = (*addHeaders)(nil)

func GetAPIClient(tb testing.TB) *api.ClientWithResponses {
	tb.Helper()

	hc := http.Client{
		Timeout: apiTimeout,
		Transport: addHeaders{
			headers: map[string]string{
				"x-test-name": tb.Name(),
			},
			rt: http.DefaultTransport,
		},
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

func WithSupabaseToken(t *testing.T, userID ...string) func(ctx context.Context, req *http.Request) error {
	t.Helper()

	if SupabaseJWTSecret == "" {
		t.Skip("Supabase JWT secret is not set")
	}

	userID_ := UserID
	if len(userID) > 0 {
		userID_ = userID[0]
	}

	token := tests.SignTestToken(t, SupabaseJWTSecret, userID_)

	return func(ctx context.Context, req *http.Request) error {
		req.Header.Set("X-Supabase-Token", token)

		return nil
	}
}

func WithSupabaseTeam(t *testing.T, teamID ...string) func(ctx context.Context, req *http.Request) error {
	t.Helper()

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
