package api_templates

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/e2b-dev/infra/packages/shared/pkg/utils"
	"github.com/e2b-dev/infra/tests/integration/internal/api"
	"github.com/e2b-dev/infra/tests/integration/internal/setup"
)

func TestRequestTemplateBuild(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](2),
		MemoryMB: utils.ToPtr[int32](1024),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusAccepted, resp.StatusCode())
}

func TestRequestTemplateTooLowCPU(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](0),
		MemoryMB: utils.ToPtr[int32](1024),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "validation error"), "error should have prefix 'validation error', the error is '%s'", resp.JSON400.Message)
}

func TestRequestTemplateTooLowRAM(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](2),
		MemoryMB: utils.ToPtr[int32](32),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "validation error"), "error should have prefix 'validation error', the error is '%s'", resp.JSON400.Message)
}

func TestRequestTemplateTooHighCPU(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](1024),
		MemoryMB: utils.ToPtr[int32](1024),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.Equal(t, "CPU count must be at most 32", resp.JSON400.Message)
}

func TestRequestTemplateOddCPU(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](3),
		MemoryMB: utils.ToPtr[int32](1024),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.Equal(t, "CPU count must be 1 or an even number", resp.JSON400.Message)
}

func TestRequestTemplateTooHighMemory(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](2),
		MemoryMB: utils.ToPtr[int32](1024 * 1024),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.True(t, strings.HasPrefix(resp.JSON400.Message, "Memory can't be higher than"), "error should have prefix 'Memory can't be higher than', the error is '%s'", resp.JSON400.Message)
}

func TestRequestTemplateMemoryNonDivisibleBy2(t *testing.T) {
	t.Parallel()
	c := setup.GetAPIClient()

	resp, err := c.PostTemplatesWithResponse(t.Context(), api.TemplateBuildRequest{
		CpuCount: utils.ToPtr[int32](2),
		MemoryMB: utils.ToPtr[int32](1001),
	}, setup.WithAccessToken())
	require.NoError(t, err)
	require.Equal(t, http.StatusBadRequest, resp.StatusCode())
	assert.Equal(t, "Memory must be divisible by 2", resp.JSON400.Message)
}
