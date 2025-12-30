package api_templates

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestListTemplatesWithAPIKey(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Test listing templates with API key authentication
	response, err := c.GetTemplatesWithResponse(
		t.Context(),
		nil,
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)

	templates := *response.JSON200
	assert.NotNil(t, templates, "Expected templates list to not be nil")
}

func TestListTemplatesWithAPIKeyAndTeamID(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Test listing templates with API key and matching team ID parameter
	response, err := c.GetTemplatesWithResponse(
		t.Context(),
		&api.GetTemplatesParams{
			TeamID: &setup.TeamID,
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)

	templates := *response.JSON200
	assert.NotNil(t, templates, "Expected templates list to not be nil")
}

func TestListTemplatesWithAPIKeyAndMismatchedTeamID(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Use a different team ID that doesn't match the API key
	mismatchedTeamID := "00000000-0000-0000-0000-000000000000"

	// Test listing templates with API key but mismatched team ID parameter
	response, err := c.GetTemplatesWithResponse(
		t.Context(),
		&api.GetTemplatesParams{
			TeamID: &mismatchedTeamID,
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	// The handler returns 400 for team ID mismatch
	require.Equal(t, http.StatusBadRequest, response.StatusCode(),
		"Expected 400 Bad Request for mismatched team ID")
}

func TestListTemplatesWithInvalidAPIKey(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	// Test listing templates with invalid API key
	response, err := c.GetTemplatesWithResponse(
		t.Context(),
		nil,
		setup.WithAPIKey("invalid-api-key"),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode())
	require.NotNil(t, response.JSON401)
}

func TestListTemplatesWithSupabaseToken(t *testing.T) {
	t.Parallel()
	// Test backward compatibility with Supabase token authentication
	c := setup.GetAPIClient()

	response, err := c.GetTemplatesWithResponse(
		t.Context(),
		nil,
		setup.WithSupabaseToken(t),
		setup.WithSupabaseTeam(t),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)

	templates := *response.JSON200
	assert.NotNil(t, templates, "Expected templates list to not be nil")
}

func TestListTemplatesWithAccessToken(t *testing.T) {
	t.Parallel()

	// Test backward compatibility with Access Token authentication
	c := setup.GetAPIClient()

	response, err := c.GetTemplatesWithResponse(
		t.Context(),
		nil,
		setup.WithAccessToken(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)

	templates := *response.JSON200
	assert.NotNil(t, templates, "Expected templates list to not be nil")
}
