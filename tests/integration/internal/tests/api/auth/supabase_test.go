package auth

import (
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	"github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func createSandbox(t *testing.T, reqEditors ...api.RequestEditorFn) int {
	t.Helper()

	ctx, cancel := context.WithCancel(t.Context())
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
	if setup.SupabaseJWTSecret == "" {
		t.Skip("Supabase JWT secret is not set")
	}

	if setup.TeamID == "" {
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

func TestSandboxCreateWithForeignTeamAccess(t *testing.T) {
	db := setup.GetTestDBClient(t)
	c := setup.GetAPIClient()

	userID2 := utils.CreateUser(t, db)
	teamID2 := utils.CreateTeamWithUser(t, c, db, "test-team-2", userID2.String())

	t.Run("Fail when using first user token with second team ID", func(t *testing.T) {
		// This should fail because the first user doesn't belong to the second team
		statusCode := createSandbox(t, setup.WithSupabaseToken(t), setup.WithSupabaseTeam(t, teamID2.String()))
		assert.Equal(t, http.StatusUnauthorized, statusCode)
	})

	t.Run("Fail when using second user token with first team ID", func(t *testing.T) {
		// This should fail because the second user doesn't belong to the first team
		statusCode := createSandbox(t, setup.WithSupabaseToken(t, userID2.String()), setup.WithSupabaseTeam(t))
		assert.Equal(t, http.StatusUnauthorized, statusCode)
	})

	t.Run("Success with second user token and second team ID", func(t *testing.T) {
		// This should succeed if the second user belongs to the second team
		statusCode := createSandbox(t, setup.WithSupabaseToken(t, userID2.String()), setup.WithSupabaseTeam(t, teamID2.String()))
		assert.Equal(t, http.StatusCreated, statusCode)
	})
}
