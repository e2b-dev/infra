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
	assert.Equal(t, http.StatusOK, deleteRes.StatusCode())
}

func TestDeleteTemplateWithAccessToken(t *testing.T) {
	alias := "test-to-delete-access-token"
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
		setup.WithAccessToken(),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, deleteRes.StatusCode())
}

func TestDeleteTemplateFromAnotherTeam(t *testing.T) {
	alias := "test-to-delete-another-team"

	db := setup.GetTestDBClient(t)
	userID := testutils.CreateUser(t, db)
	accessToken := testutils.CreateAccessToken(t, db, userID)

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
		setup.WithCustomAccessToken(accessToken),
	)
	require.NoError(t, err)
	assert.Equal(t, http.StatusForbidden, deleteRes.StatusCode())
}
