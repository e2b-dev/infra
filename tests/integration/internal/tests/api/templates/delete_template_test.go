package api_templates

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	testutils "github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestDeleteTemplate(t *testing.T) {
	t.Parallel()
	alias := "test-to-delete"
	res := buildTemplate(t, alias, api.TemplateBuildStartV2{
		Force:     utils.ToPtr(ForceBaseBuild),
		FromImage: utils.ToPtr("ubuntu:22.04"),
		Steps: utils.ToPtr([]api.TemplateStep{
			{
				Type:  "RUN",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"echo 'Hello, World!'"}),
			},
		}),
	}, defaultBuildLogHandler(t))

	require.True(t, res)

	c := setup.GetAPIClient()
	deleteRes, err := c.DeleteTemplatesTemplateIDWithResponse(
		t.Context(),
		alias,
		setup.WithAPIKey(),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, deleteRes.StatusCode())
}

func TestDeleteTemplateWithAccessToken(t *testing.T) {
	t.Parallel()

	// Build template and get the template ID
	template := testutils.BuildSimpleTemplate(t, "test-to-delete-access-token", setup.WithAPIKey())

	c := setup.GetAPIClient()
	// Access token auth requires template ID (not alias)
	deleteRes, err := c.DeleteTemplatesTemplateIDWithResponse(
		t.Context(),
		template.TemplateID,
		setup.WithAccessToken(),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, deleteRes.StatusCode())
}

func TestDeleteTemplateFromAnotherTeamAccessToken(t *testing.T) {
	t.Parallel()

	db := setup.GetTestDBClient(t)
	userID := testutils.CreateUser(t, db)
	accessToken := testutils.CreateAccessToken(t, db, userID)

	// Build template with default API key (belongs to default team)
	template := testutils.BuildSimpleTemplate(t, "test-to-delete-another-team-access-token", setup.WithAPIKey())

	c := setup.GetAPIClient()
	// Access token auth requires template ID; this user has no teams so should get 403
	deleteRes, err := c.DeleteTemplatesTemplateIDWithResponse(
		t.Context(),
		template.TemplateID,
		setup.WithCustomAccessToken(accessToken),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, deleteRes.StatusCode())
}

func TestDeleteTemplateFromAnotherTeamAPIKey(t *testing.T) {
	t.Parallel()
	alias := "test-to-delete-another-team-api-key"

	db := setup.GetTestDBClient(t)
	userID := testutils.CreateUser(t, db)
	teamID := testutils.CreateTeamWithUser(t, db, "foreign-team", userID.String())
	apiKey := testutils.CreateAPIKey(t, t.Context(), setup.GetAPIClient(), userID.String(), teamID)

	res := buildTemplate(t, alias, api.TemplateBuildStartV2{
		Force:     utils.ToPtr(ForceBaseBuild),
		FromImage: utils.ToPtr("ubuntu:22.04"),
		Steps: utils.ToPtr([]api.TemplateStep{
			{
				Type:  "RUN",
				Force: utils.ToPtr(true),
				Args:  utils.ToPtr([]string{"echo 'Hello, World!'"}),
			},
		}),
	}, defaultBuildLogHandler(t))
	require.True(t, res)

	c := setup.GetAPIClient()
	deleteRes, err := c.DeleteTemplatesTemplateIDWithResponse(
		t.Context(),
		alias,
		setup.WithAPIKey(apiKey),
	)
	require.NoError(t, err)
	// With namespace-scoped templates, the foreign team's lookup searches in their own namespace
	// and doesn't find the template (which exists in the original team's namespace), so 404 is returned.
	assert.Equal(t, http.StatusNotFound, deleteRes.StatusCode())
}
