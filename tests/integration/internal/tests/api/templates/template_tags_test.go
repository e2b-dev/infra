package api_templates

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
	testutils "github.com/e2b-dev/infra/tests/integration/internal/utils"
)

func TestTemplateTagAssign(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, "test-tag-assign", setup.WithAPIKey())

	// Assign a custom tag to the template
	tagResp, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
		Tag: "v1",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Tag response: %s", string(tagResp.Body))
		}
	})

	assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
	require.NotNil(t, tagResp.JSON201)
	assert.Equal(t, "v1", tagResp.JSON201.Tag)
	assert.Equal(t, template.BuildID, tagResp.JSON201.BuildID)
}

func TestTemplateTagAssignFromSourceTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, "test-tag-source", setup.WithAPIKey())

	// First assign a source tag
	_, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
		Tag: "staging",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	// Assign a new tag from the source tag
	sourceTag := "staging"
	tagResp, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
		SourceTag: &sourceTag,
		Tag:       "production",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
	require.NotNil(t, tagResp.JSON201)
	assert.Equal(t, "production", tagResp.JSON201.Tag)
	// Both tags should point to the same build
	assert.Equal(t, template.BuildID, tagResp.JSON201.BuildID)
}

func TestTemplateTagDelete(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, "test-tag-delete", setup.WithAPIKey())

	// Assign a tag
	_, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
		Tag: "to-delete",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	// Delete the tag
	deleteResp, err := c.DeleteTemplatesTemplateIDTagsTagWithResponse(ctx, template.TemplateID, "to-delete", setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusNoContent, deleteResp.StatusCode())
}

func TestTemplateTagDeleteLatestNotAllowed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, "test-tag-delete-latest", setup.WithAPIKey())

	// Try to delete the 'default' tag - should fail
	deleteResp, err := c.DeleteTemplatesTemplateIDTagsTagWithResponse(ctx, template.TemplateID, "default", setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, deleteResp.StatusCode())
}

func TestSandboxCreateWithTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template
	template := testutils.BuildSimpleTemplate(t, "test-sbx-tag", setup.WithAPIKey())

	// Assign a version tag
	_, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
		Tag: "v1.0",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	// Create a sandbox using the tagged template (templateID:tag format)
	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: template.TemplateID + ":v1.0",
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}

		if resp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, resp.JSON201.SandboxID)
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	require.NotNil(t, resp.JSON201)
}

func TestSandboxCreateWithLatestTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Create a sandbox using explicit :latest tag - should work same as without tag
	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID + ":latest",
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}

		if resp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, resp.JSON201.SandboxID)
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
}

func TestSandboxCreateWithNonExistentTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Try to create a sandbox with a non-existent tag
	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID + ":nonexistent-tag",
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, resp.StatusCode())
}

func TestSandboxCreateWithAliasAndTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template with a specific alias
	alias := "test-alias-tag"
	template := testutils.BuildSimpleTemplate(t, alias, setup.WithAPIKey())

	// Assign a version tag
	_, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
		Tag: "stable",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	// Create a sandbox using alias:tag format
	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: alias + ":stable",
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(resp.Body))
		}

		if resp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, resp.JSON201.SandboxID)
		}
	})

	assert.Equal(t, http.StatusCreated, resp.StatusCode())
	require.NotNil(t, resp.JSON201)
}

func TestTemplateTagNotFoundForNonExistentTemplate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Try to assign a tag to a non-existent template
	tagResp, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, "nonexistent-template-id", api.AssignTemplateTagRequest{
		Tag: "v1",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusNotFound, tagResp.StatusCode())
}

func TestTemplateTagInvalidTagName(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	c := setup.GetAPIClient()

	// Try to assign a tag with empty name
	tagResp, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, setup.SandboxTemplateID, api.AssignTemplateTagRequest{
		Tag: "",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, tagResp.StatusCode())
}

func TestMultipleTagsOnSameTemplate(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template
	template := testutils.BuildSimpleTemplate(t, "test-multi-tags", setup.WithAPIKey())

	// Assign multiple tags to the same build
	tags := []string{"dev", "staging", "prod", "v1.0.0"}
	for _, tag := range tags {
		tagResp, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template.TemplateID, api.AssignTemplateTagRequest{
			Tag: tag,
		}, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
		assert.Equal(t, template.BuildID, tagResp.JSON201.BuildID)
	}

	// Verify we can create sandboxes with each tag
	for _, tag := range tags {
		sbxTimeout := int32(60)
		resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
			TemplateID: template.TemplateID + ":" + tag,
			Timeout:    &sbxTimeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)

		if resp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, resp.JSON201.SandboxID)
		}

		assert.Equal(t, http.StatusCreated, resp.StatusCode(), "Failed to create sandbox with tag: %s", tag)
	}
}

func TestTagReassignment(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build first version of template
	template1 := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Alias: "test-tag-reassign",
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
			Steps: utils.ToPtr([]api.TemplateStep{
				{
					Type:  "ENV",
					Force: utils.ToPtr(true),
					Args:  utils.ToPtr([]string{"VERSION", "1"}),
				},
			}),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Assign 'stable' tag to first build
	_, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template1.TemplateID, api.AssignTemplateTagRequest{
		Tag: "stable",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	// Build second version
	template2 := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Alias: "test-tag-reassign",
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
			Steps: utils.ToPtr([]api.TemplateStep{
				{
					Type:  "ENV",
					Force: utils.ToPtr(true),
					Args:  utils.ToPtr([]string{"VERSION", "2"}),
				},
			}),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Reassign 'stable' tag to second build (by using 'default' as source)
	tagResp, err := c.PostTemplatesTemplateIDTagsWithResponse(ctx, template2.TemplateID, api.AssignTemplateTagRequest{
		Tag: "stable",
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
	assert.Equal(t, template2.BuildID, tagResp.JSON201.BuildID, "stable tag should now point to second build")
}

func TestTemplateBuildWithTags(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template with tags specified during creation
	tags := []string{"v1.0.0", "stable"}
	template := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Alias: "test-build-with-tags",
		Tags:  tags,
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Verify we can create sandboxes with each tag that was specified during creation
	for _, tag := range tags {
		sbxTimeout := int32(60)
		sbxResp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
			TemplateID: template.TemplateID + ":" + tag,
			Timeout:    &sbxTimeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)

		if sbxResp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, sbxResp.JSON201.SandboxID)
		}

		assert.Equal(t, http.StatusCreated, sbxResp.StatusCode(), "Failed to create sandbox with tag: %s", tag)
	}
}

func TestTemplateBuildWithTagsAndSandboxCreation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template with 'production' tag specified during creation
	template := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Alias: "test-prod-tag",
		Tags:  []string{"production"},
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Create sandbox using alias:tag format
	sbxTimeout := int32(60)
	sbxResp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: template.TemplateID + ":production",
		Timeout:    &sbxTimeout,
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Response: %s", string(sbxResp.Body))
		}

		if sbxResp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, sbxResp.JSON201.SandboxID)
		}
	})

	assert.Equal(t, http.StatusCreated, sbxResp.StatusCode())
	require.NotNil(t, sbxResp.JSON201)
}
