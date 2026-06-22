package api_templates

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestListTemplatesV2WithAPIKey(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	response, err := c.GetV2TemplatesWithResponse(
		t.Context(),
		&api.GetV2TemplatesParams{},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)

	assert.NotNil(t, *response.JSON200, "Expected templates list to not be nil")
}

func TestListTemplatesV2WithAPIKeyAndTeamID(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	response, err := c.GetV2TemplatesWithResponse(
		t.Context(),
		&api.GetV2TemplatesParams{
			TeamID: &setup.TeamID,
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, response.StatusCode())
	require.NotNil(t, response.JSON200)
}

func TestListTemplatesV2WithMismatchedTeamID(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	mismatchedTeamID := "00000000-0000-0000-0000-000000000000"

	response, err := c.GetV2TemplatesWithResponse(
		t.Context(),
		&api.GetV2TemplatesParams{
			TeamID: &mismatchedTeamID,
		},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, response.StatusCode(),
		"Expected 400 Bad Request for mismatched team ID")
}

func TestListTemplatesV2WithInvalidAPIKey(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	response, err := c.GetV2TemplatesWithResponse(
		t.Context(),
		&api.GetV2TemplatesParams{},
		setup.WithAPIKey("invalid-api-key"),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusUnauthorized, response.StatusCode())
	require.NotNil(t, response.JSON401)
}

// TestListTemplatesV2Pagination verifies the limit + nextToken wiring: a page is
// capped at the requested limit, and any X-Next-Token returned can be used to
// fetch the next page.
func TestListTemplatesV2Pagination(t *testing.T) {
	t.Parallel()

	c := setup.GetAPIClient()

	var limit int32 = 1
	firstPage, err := c.GetV2TemplatesWithResponse(
		t.Context(),
		&api.GetV2TemplatesParams{Limit: &limit},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, firstPage.StatusCode())
	require.NotNil(t, firstPage.JSON200)
	assert.LessOrEqual(t, len(*firstPage.JSON200), int(limit), "page must not exceed the requested limit")

	nextToken := firstPage.HTTPResponse.Header.Get("X-Next-Token")
	if nextToken == "" {
		// Fewer than `limit`+1 templates exist for this team; nothing more to page.
		return
	}

	secondPage, err := c.GetV2TemplatesWithResponse(
		t.Context(),
		&api.GetV2TemplatesParams{Limit: &limit, NextToken: &nextToken},
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, secondPage.StatusCode())
	require.NotNil(t, secondPage.JSON200)
	assert.LessOrEqual(t, len(*secondPage.JSON200), int(limit))
}
