package templatecache

import (
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestErrorToAPIError_TagNotFound_WithTag(t *testing.T) {
	t.Parallel()

	tag := "v2"
	apiErr := ErrorToAPIErrorWithTemplate(ErrTemplateTagNotFound, "mytemplate", "tmpl-abc", &tag)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' (tmpl-abc) with tag 'v2' not found", apiErr.ClientMsg)
	assert.ErrorIs(t, apiErr.Err, ErrTemplateTagNotFound)
}

func TestErrorToAPIError_TagNotFound_WithoutTag(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIErrorWithTemplate(ErrTemplateTagNotFound, "mytemplate", "tmpl-abc", nil)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' (tmpl-abc) has no ready build", apiErr.ClientMsg)
}

func TestErrorToAPIError_TemplateNotFound_IdentifierOnly(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIError(ErrTemplateNotFound, "mytemplate")

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' not found", apiErr.ClientMsg)
}

func TestErrorToAPIError_TemplateNotFound_UndisclosedOmitsTemplateID(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIErrorWithTemplate(ErrTemplateNotFoundUndisclosed, "mytemplate", "tmpl-abc", nil)

	assert.Equal(t, http.StatusNotFound, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' not found", apiErr.ClientMsg)
	assert.ErrorIs(t, apiErr.Err, ErrTemplateNotFoundUndisclosed)
	assert.ErrorIs(t, apiErr.Err, ErrTemplateNotFound)
}

func TestErrorToAPIError_FormatTemplateRef_IdentifierEqualsTemplateID(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIErrorWithTemplate(ErrTemplateNotFound, "tmpl-abc", "tmpl-abc", nil)

	assert.Equal(t, "template 'tmpl-abc' not found", apiErr.ClientMsg)
}

func TestErrorToAPIError_AccessDenied(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIErrorWithTemplate(ErrAccessDenied, "mytemplate", "tmpl-abc", nil)

	assert.Equal(t, http.StatusForbidden, apiErr.Code)
	assert.Equal(t, "you don't have access to template 'mytemplate' (tmpl-abc)", apiErr.ClientMsg)
}

func TestErrorToAPIError_ClusterMismatch(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIErrorWithTemplate(ErrClusterMismatch, "mytemplate", "tmpl-abc", nil)

	assert.Equal(t, http.StatusBadRequest, apiErr.Code)
	assert.Equal(t, "template 'mytemplate' (tmpl-abc) is not available in the requested cluster", apiErr.ClientMsg)
}

func TestErrorToAPIError_Unknown(t *testing.T) {
	t.Parallel()

	apiErr := ErrorToAPIError(errors.New("boom"), "mytemplate")

	assert.Equal(t, http.StatusInternalServerError, apiErr.Code)
}
