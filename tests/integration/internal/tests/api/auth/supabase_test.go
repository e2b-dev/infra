package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"

	"github.com/stretchr/testify/assert"
)

func createSandbox(t *testing.T, reqEditors ...api.RequestEditorFn) int {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(10)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, reqEditors...)
	assert.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	return resp.StatusCode()
}

func TestSandboxCreateWithSupabaseToken(t *testing.T) {
	if setup.SupabaseToken == "" {
		t.Skip("Supabase token is not set")
	}

	if setup.SupabaseTeamID == "" {
		t.Skip("Supabase team ID is not set")
	}

	t.Run("Create sandbox with Supabase token", func(t *testing.T) {
		statusCode := createSandbox(t, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))

		assert.Equal(t, http.StatusCreated, statusCode)
	})

	t.Run("Fail creation with missing teamID", func(t *testing.T) {
		statusCode := createSandbox(t, setup.WithSupabaseToken(t))
		assert.Equal(t, http.StatusUnauthorized, statusCode)
	})

	t.Run("Fail creation with missing token", func(t *testing.T) {
		statusCode := createSandbox(t, setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusUnauthorized, statusCode)
	})

	t.Run("Fail creation with invalid token", func(t *testing.T) {
		statusCode := createSandbox(t, func(ctx context.Context, req *http.Request) error {
			req.Header.Set("X-Supabase-Token", "invalid")

			return nil
		}, setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusUnauthorized, statusCode)
	})
}
