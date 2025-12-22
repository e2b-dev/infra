package api_templates

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/id"
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
	template := testutils.BuildSimpleTemplate(t, []string{"test-tag-assign"}, setup.WithAPIKey())

	// Assign a custom tag to the template
	// POST /templates/tags/{source} with body { names: [targets] }
	tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{template.TemplateID + ":v1"},
	}, setup.WithAPIKey())
	require.NoError(t, err)

	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("Tag response: %s", string(tagResp.Body))
		}
	})

	assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
	require.NotNil(t, tagResp.JSON201)
	assert.Equal(t, []string{"v1"}, tagResp.JSON201.Tags)
	assert.Equal(t, template.BuildID, tagResp.JSON201.BuildID.String())
}

func TestTemplateTagAssignFromSourceTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, []string{"test-tag-source"}, setup.WithAPIKey())

	// First assign a source tag (from the default build)
	sourceTag := "staging"
	stagingResp, err := c.PostTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{template.TemplateID + ":" + sourceTag},
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, stagingResp.StatusCode())

	// Assign a new tag from the source tag (staging)
	prodTag := "production"
	tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+sourceTag, api.AssignTemplateTagRequest{
		Names: []string{template.TemplateID + ":" + prodTag},
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
	require.NotNil(t, tagResp.JSON201)
	assert.Equal(t, []string{prodTag}, tagResp.JSON201.Tags)
	// Both tags should point to the same build
	assert.Equal(t, template.BuildID, tagResp.JSON201.BuildID.String())
}

func TestTemplateTagDelete(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, []string{"test-tag-delete"}, setup.WithAPIKey())

	// Assign a tag
	_, err := c.PostTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{template.TemplateID + ":to-delete"},
	}, setup.WithAPIKey())
	require.NoError(t, err)

	// Delete the tag - DELETE /templates/tags/{name}
	deleteResp, err := c.DeleteTemplatesTagsNameWithResponse(ctx, template.TemplateID+":to-delete", setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusNoContent, deleteResp.StatusCode())
}

func TestTemplateTagDeleteLatestNotAllowed(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template to work with
	template := testutils.BuildSimpleTemplate(t, []string{"test-tag-delete-latest"}, setup.WithAPIKey())

	// Try to delete the 'default' tag - should fail
	deleteResp, err := c.DeleteTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+id.DefaultTag, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusBadRequest, deleteResp.StatusCode())
}

func TestSandboxCreateWithTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template
	template := testutils.BuildSimpleTemplate(t, []string{"test-sbx-tag"}, setup.WithAPIKey())

	// Assign a version tag
	_, err := c.PostTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{template.TemplateID + ":v1.0"},
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

func TestSandboxCreateWithDefaultTag(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Create a sandbox using explicit :default tag - should work same as without tag
	sbxTimeout := int32(60)
	resp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: setup.SandboxTemplateID + ":" + id.DefaultTag,
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
	testutils.BuildSimpleTemplate(t, []string{alias}, setup.WithAPIKey())

	// Assign a version tag using the alias
	tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, alias+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{alias + ":stable"},
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, tagResp.StatusCode())

	// Create a sandbox using alias:tag format (API should resolve alias to templateID)
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
	tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, "nonexistent-template-id:"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{"nonexistent-template-id:v1"},
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
	tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, setup.SandboxTemplateID+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{""},
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
	template := testutils.BuildSimpleTemplate(t, []string{"test-multi-tags"}, setup.WithAPIKey())

	// Assign multiple tags to the same build
	tags := []string{"dev", "staging", "prod", "v1.0.0"}
	for _, tag := range tags {
		tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, template.TemplateID+":"+id.DefaultTag, api.AssignTemplateTagRequest{
			Names: []string{template.TemplateID + ":" + tag},
		}, setup.WithAPIKey())
		require.NoError(t, err)
		assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
		assert.Equal(t, template.BuildID, tagResp.JSON201.BuildID.String())
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

	alias := "test-tag-reassign"

	// Build first version of template
	template1 := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Names: []string{alias},
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

	// Assign 'stable' tag to first build using the alias
	stableResp, err := c.PostTemplatesTagsNameWithResponse(ctx, alias+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{alias + ":stable"},
	}, setup.WithAPIKey())
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, stableResp.StatusCode())

	// Build second version (will use the same template since same alias)
	template2 := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Names: []string{alias},
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

	// Both builds should have the same template ID since they use the same alias
	require.Equal(t, template1.TemplateID, template2.TemplateID, "Both builds should be for the same template")

	// Reassign 'stable' tag to second build (by using 'default' as source)
	tagResp, err := c.PostTemplatesTagsNameWithResponse(ctx, alias+":"+id.DefaultTag, api.AssignTemplateTagRequest{
		Names: []string{alias + ":stable"},
	}, setup.WithAPIKey())
	require.NoError(t, err)

	assert.Equal(t, http.StatusCreated, tagResp.StatusCode())
	require.NotNil(t, tagResp.JSON201)
	assert.Equal(t, template2.BuildID, tagResp.JSON201.BuildID.String(), "stable tag should now point to second build")
}

func TestTemplateBuildWithTags(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template with tags specified during creation
	names := []string{"test-build-with-tags:v1.0.0", "test-build-with-tags:stable"}
	testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Names: names,
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Verify we can create sandboxes with each tag that was specified during creation
	for _, name := range names {
		sbxTimeout := int32(60)
		sbxResp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
			TemplateID: name,
			Timeout:    &sbxTimeout,
		}, setup.WithAPIKey())
		require.NoError(t, err)

		if sbxResp.JSON201 != nil {
			testutils.TeardownSandbox(t, c, sbxResp.JSON201.SandboxID)
		}

		assert.Equal(t, http.StatusCreated, sbxResp.StatusCode(), "Failed to create sandbox with tag: %s", name)
	}
}

func TestTemplateBuildWithTagsAndSandboxCreation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	name := "test-prod-tag:production"

	// Build a template with 'production' tag specified during creation
	testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Names: []string{name},
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Create sandbox using alias:tag format
	sbxTimeout := int32(60)
	sbxResp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: name,
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

func TestTemplateBuildWithTagInAlias(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()

	c := setup.GetAPIClient()

	// Build a template with tag in alias (e.g., "my-alias:my-tag")
	// Without providing explicit tags in the request body
	// The tag from the alias should be used automatically
	template := testutils.BuildTemplate(t, testutils.TemplateBuildOptions{
		Names: []string{"test-alias-with-tag:v2.0"},
		// Tags: intentionally not provided - should be inferred from alias
		BuildData: api.TemplateBuildStartV2{
			FromImage: utils.ToPtr("ubuntu:22.04"),
		},
		ReqEditors: []api.RequestEditorFn{setup.WithAPIKey()},
	})

	// Create sandbox using the tag that was embedded in the alias
	sbxTimeout := int32(60)
	sbxResp, err := c.PostSandboxesWithResponse(ctx, api.NewSandbox{
		TemplateID: template.TemplateID + ":v2.0",
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
