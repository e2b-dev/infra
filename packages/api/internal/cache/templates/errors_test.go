package templatecache

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTemplateRef_APIError_MissingBuildWithTag(t *testing.T) {
	t.Parallel()

	tag := "v2"
	apiErr := TemplateRef{
		Subject:    "template",
		Identifier: "mytemplate",
		TemplateID: "tmpl-abc",
		Tag:        &tag,
		Visible:    true,
	}.APIError(ErrTemplateNotFound)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' (tmpl-abc) with tag 'v2' not found", apiErr.ClientMsg)
}

func TestTemplateRef_APIError_MissingBuildWithoutTag(t *testing.T) {
	t.Parallel()

	apiErr := TemplateRef{
		Subject:    "template",
		Identifier: "mytemplate",
		TemplateID: "tmpl-abc",
		Visible:    true,
	}.APIError(ErrTemplateNotFound)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' (tmpl-abc) has no ready build", apiErr.ClientMsg)
}

func TestTemplateRef_APIError_HiddenTemplate(t *testing.T) {
	t.Parallel()

	tag := "v2"
	apiErr := TemplateRef{
		Subject:    "template",
		Identifier: "mytemplate",
		TemplateID: "tmpl-abc",
		Tag:        &tag,
		Visible:    false,
	}.APIError(ErrTemplateNotFound)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' not found", apiErr.ClientMsg)
}

func TestErrorToAPIError_TemplateNotFound(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIError(ErrTemplateNotFound, "mytemplate")

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' not found", apiErr.ClientMsg)
}

func TestErrorToAPIError_FormatTemplateRef_IdentifierEqualsTemplateID(t *testing.T) {
	t.Parallel()

	apiErr := TemplateRef{
		Subject:    "template",
		Identifier: "tmpl-abc",
		TemplateID: "tmpl-abc",
		Visible:    true,
	}.APIError(ErrTemplateNotFound)

	assert.Equal(t, "template 'tmpl-abc' has no ready build", apiErr.ClientMsg)
}

func TestErrorToAPIError_AccessDenied(t *testing.T) {
	t.Parallel()

	apiErr := ToAPIError(ErrAccessDenied, "template", "mytemplate")

	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	assert.Equal(t, "you don't have access to template 'mytemplate'", apiErr.ClientMsg)
}

func TestErrorToAPIError_ClusterMismatch(t *testing.T) {
	t.Parallel()

	apiErr := ToAPIError(ErrClusterMismatch, "template", "mytemplate")

	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' is not available in the requested cluster", apiErr.ClientMsg)
}

func TestTemplateRef_APIError_BaseTemplateUsesSubject(t *testing.T) {
	t.Parallel()

	tag := "v2"
	apiErr := TemplateRef{
		Subject:    "base template",
		Identifier: "mytemplate",
		TemplateID: "tmpl-abc",
		Tag:        &tag,
		Visible:    true,
	}.APIError(ErrTemplateNotFound)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "base template 'mytemplate' (tmpl-abc) with tag 'v2' not found", apiErr.ClientMsg)
}

func TestErrorToAPIError_Unknown(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIError(errors.New("boom"), "mytemplate")

	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}
