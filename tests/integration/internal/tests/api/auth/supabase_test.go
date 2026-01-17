package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func createSandbox(t *testing.T, reqEditors ...api.RequestEditorFn) *api.PostSandboxesResponse {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	sbxTimeout := int32(10)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID,
		Timeout:    &sbxTimeout,
	}, reqEditors...)
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}
	})

	return resp
}

func TestSandboxCreateWithSupabaseToken(t *testing.T) {
	t.Parallel()
	if setup.SupabaseJWTSecret == "" {
		t.Skip("Supabase JWT secret is not set")
	}

	if setup.TeamID == "" {
		t.Skip("Supabase team ID is not set")
	}

	t.Run("Create sandbox with Supabase token", func(t *testing.T) {
		t.Parallel()
		response := createSandbox(t, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	})

	t.Run("Fail creation with missing teamID", func(t *testing.T) {
		t.Parallel()
		response := createSandbox(t, setup.WithSupabaseToken(t))
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode(), string(response.Body))
	})

	t.Run("Fail creation with missing token", func(t *testing.T) {
		t.Parallel()
		response := createSandbox(t, setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode(), string(response.Body))
	})

	t.Run("Fail creation with invalid token", func(t *testing.T) {
		t.Parallel()
		response := createSandbox(t, func(_ context.Context, req *http.Request) error {
			req.Header.Set("X-Supabase-Token", "invalid")

			return nil
		}, setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode(), string(response.Body))
	})
}

func TestSandboxCreateWithForeignTeamAccess(t *testing.T) {
	t.Parallel()
	db := setup.GetTestDBClient(t)

	userID2 := utils.CreateUser(t, db)
	teamID2 := utils.CreateTeamWithUser(t, db, "test-team-2", userID2.String())

	t.Run("Fail when using first user token with second team ID", func(t *testing.T) {
		t.Parallel()
		// This should fail because the first user doesn't belong to the second team
		response := createSandbox(t, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID2.String()))
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode(), string(response.Body))
	})

	t.Run("Fail when using second user token with first team ID", func(t *testing.T) {
		t.Parallel()
		// This should fail because the second user doesn't belong to the first team
		response := createSandbox(t, setup.WithSupabaseToken(t, userID2.String()), setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusUnauthorized, response.StatusCode(), string(response.Body))
	})

	t.Run("Success with second user token and second team ID", func(t *testing.T) {
		t.Parallel()
		// This should succeed if the second user belongs to the second team
		response := createSandbox(t, setup.WithSupabaseToken(t, userID2.String()), setup.WithSupabaseTeam(t, teamID2.String()))
		assert.Equal(t, http.StatusCreated, response.StatusCode(), string(response.Body))
	})
}
